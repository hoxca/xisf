package xisf

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file implements the lossless XML layer. The XISF header is an XML
// document, and a reader that re-renders it from a typed model loses things
// the format is explicitly extensible about: unknown elements, attribute
// order, comments, and the exact text of properties. PixInsight headers can
// carry hundreds of kilobytes of escaped processing history.
//
// Instead every node records the byte range it occupies in the source, and
// rendering copies untouched ranges verbatim. Only nodes whose attributes or
// text have been changed are rebuilt.

type span struct {
	start, end int
}

type xmlAttr struct {
	name     string // as written, including any prefix
	value    string // XML-unescaped
	rawValue string // exactly as it appears between the quotes
	quote    byte
	dirty    bool
}

type xmlContent struct {
	text  *span // raw character data, comment, CDATA or processing instruction
	own   *string
	child *xmlNode
}

type xmlNode struct {
	name             string // as written, including any prefix
	attrs            []xmlAttr
	content          []xmlContent
	rawStart, rawEnd int
	parent           *xmlNode
	dirty            bool
	text             *string // when non-nil, replaces all content with this character data
}

type xmlDoc struct {
	src    []byte
	root   *xmlNode
	prefix []byte // declaration, initial comment, whitespace before the root
	suffix []byte // anything after the root, e.g. an XML signature
}

func (n *xmlNode) localName() string {
	if i := strings.IndexByte(n.name, ':'); i >= 0 {
		return n.name[i+1:]
	}
	return n.name
}

func (n *xmlNode) attr(name string) *xmlAttr {
	for i := range n.attrs {
		if n.attrs[i].name == name {
			return &n.attrs[i]
		}
	}
	return nil
}

func (n *xmlNode) attrValue(name string) (string, bool) {
	if a := n.attr(name); a != nil {
		return a.value, true
	}
	return "", false
}

// setAttr updates an existing attribute or appends a new one. The value is
// stored unescaped and escaped again when the header is rendered.
func (n *xmlNode) setAttr(name, value string) {
	if a := n.attr(name); a != nil {
		if a.value == value {
			return
		}
		a.value = value
		a.rawValue = escapeAttr(value)
		a.dirty = true
		return
	}
	n.attrs = append(n.attrs, xmlAttr{
		name:     name,
		value:    value,
		rawValue: escapeAttr(value),
		quote:    '"',
		dirty:    true,
	})
}

func (n *xmlNode) removeAttr(name string) {
	for i := range n.attrs {
		if n.attrs[i].name == name {
			n.attrs = append(n.attrs[:i], n.attrs[i+1:]...)
			n.dirty = true
			return
		}
	}
}

// children returns the direct child elements in document order.
func (n *xmlNode) children() []*xmlNode {
	var out []*xmlNode
	for _, c := range n.content {
		if c.child != nil {
			out = append(out, c.child)
		}
	}
	return out
}

func (n *xmlNode) child(name string) *xmlNode {
	for _, c := range n.children() {
		if c.localName() == name {
			return c
		}
	}
	return nil
}

func (n *xmlNode) subtreeDirty() bool {
	if n.dirty || n.text != nil {
		return true
	}
	for i := range n.attrs {
		if n.attrs[i].dirty {
			return true
		}
	}
	for _, c := range n.content {
		if c.child != nil && c.child.subtreeDirty() {
			return true
		}
	}
	return false
}

// textValue returns the character data of an element, unescaped, with
// comments, CDATA markers and processing instructions handled.
func (n *xmlNode) textValue(src []byte) string {
	if n.text != nil {
		return *n.text
	}
	var b strings.Builder
	for _, c := range n.content {
		if c.own != nil {
			b.WriteString(*c.own)
			continue
		}
		if c.text == nil {
			continue
		}
		raw := string(src[c.text.start:c.text.end])
		switch {
		case strings.HasPrefix(raw, "<!--"), strings.HasPrefix(raw, "<?"):
			// not character data
		case strings.HasPrefix(raw, "<![CDATA["):
			raw = strings.TrimSuffix(strings.TrimPrefix(raw, "<![CDATA["), "]]>")
			b.WriteString(raw)
		default:
			b.WriteString(xmlUnescape(raw))
		}
	}
	return b.String()
}

func (n *xmlNode) setText(s string) {
	n.text = &s
}

// --- parsing ---------------------------------------------------------------

type xmlParser struct {
	src []byte
	pos int
}

func parseXML(src []byte) (*xmlDoc, error) {
	p := &xmlParser{src: src}
	// Skip a UTF-8 byte order mark; it is preserved in the document prefix.
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		p.pos = 3
	}
	if err := p.skipMisc(); err != nil {
		return nil, err
	}
	if p.eof() || p.src[p.pos] != '<' {
		return nil, errf(ErrHeader, "XML document has no root element")
	}
	root, err := p.parseElement()
	if err != nil {
		return nil, err
	}
	doc := &xmlDoc{
		src:    src,
		root:   root,
		prefix: append([]byte(nil), src[:root.rawStart]...),
		suffix: append([]byte(nil), src[root.rawEnd:]...),
	}
	return doc, nil
}

func (p *xmlParser) eof() bool { return p.pos >= len(p.src) }

// skipMisc consumes whitespace, XML declarations, comments, processing
// instructions and a doctype before the root element.
func (p *xmlParser) skipMisc() error {
	for {
		for !p.eof() {
			switch p.src[p.pos] {
			case ' ', '\t', '\r', '\n':
				p.pos++
			default:
				goto done
			}
		}
	done:
		if p.eof() || p.src[p.pos] != '<' {
			return nil
		}
		switch {
		case p.hasPrefix("<?"):
			if err := p.skipUntil("?>"); err != nil {
				return err
			}
		case p.hasPrefix("<!--"):
			if err := p.skipUntil("-->"); err != nil {
				return err
			}
		case p.hasPrefix("<!DOCTYPE"), p.hasPrefix("<!doctype"):
			// A doctype is not expected in XISF, but skip it rather than fail.
			if err := p.skipDOCTYPE(); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (p *xmlParser) hasPrefix(s string) bool {
	return strings.HasPrefix(string(p.src[p.pos:]), s)
}

func (p *xmlParser) skipUntil(term string) error {
	i := bytes.Index(p.src[p.pos:], []byte(term))
	if i < 0 {
		return errf(ErrHeader, "unterminated %q", term)
	}
	p.pos += i + len(term)
	return nil
}

func (p *xmlParser) skipDOCTYPE() error {
	depth := 0
	for !p.eof() {
		switch p.src[p.pos] {
		case '[':
			depth++
		case ']':
			depth--
		case '>':
			if depth <= 0 {
				p.pos++
				return nil
			}
		}
		p.pos++
	}
	return errf(ErrHeader, "unterminated DOCTYPE")
}

func (p *xmlParser) parseElement() (*xmlNode, error) {
	start := p.pos
	if p.eof() || p.src[p.pos] != '<' {
		return nil, errf(ErrHeader, "expected an element at byte %d", p.pos)
	}
	p.pos++
	name, err := p.parseName()
	if err != nil {
		return nil, err
	}
	n := &xmlNode{name: name, rawStart: start}

	for {
		p.skipSpace()
		if p.eof() {
			return nil, errf(ErrHeader, "unterminated start tag <%s>", name)
		}
		switch p.src[p.pos] {
		case '/':
			p.pos++
			if p.eof() || p.src[p.pos] != '>' {
				return nil, errf(ErrHeader, "malformed empty-element tag <%s>", name)
			}
			p.pos++
			n.rawEnd = p.pos
			return n, nil
		case '>':
			p.pos++
			if err := p.parseContent(n); err != nil {
				return nil, err
			}
			n.rawEnd = p.pos
			return n, nil
		default:
			a, err := p.parseAttr()
			if err != nil {
				return nil, err
			}
			n.attrs = append(n.attrs, a)
		}
	}
}

func (p *xmlParser) parseContent(parent *xmlNode) error {
	for {
		if p.eof() {
			return errf(ErrHeader, "unterminated element <%s>", parent.name)
		}
		if p.hasPrefix("</") {
			p.pos += 2
			name, err := p.parseName()
			if err != nil {
				return err
			}
			if name != parent.name {
				return errf(ErrHeader, "end tag </%s> does not match <%s>", name, parent.name)
			}
			p.skipSpace()
			if p.eof() || p.src[p.pos] != '>' {
				return errf(ErrHeader, "malformed end tag </%s>", name)
			}
			p.pos++
			return nil
		}
		if p.hasPrefix("<!--") {
			start := p.pos
			if err := p.skipUntil("-->"); err != nil {
				return err
			}
			parent.content = append(parent.content, xmlContent{text: &span{start, p.pos}})
			continue
		}
		if p.hasPrefix("<![CDATA[") {
			start := p.pos
			if err := p.skipUntil("]]>"); err != nil {
				return err
			}
			parent.content = append(parent.content, xmlContent{text: &span{start, p.pos}})
			continue
		}
		if p.hasPrefix("<?") {
			start := p.pos
			if err := p.skipUntil("?>"); err != nil {
				return err
			}
			parent.content = append(parent.content, xmlContent{text: &span{start, p.pos}})
			continue
		}
		if p.src[p.pos] == '<' {
			child, err := p.parseElement()
			if err != nil {
				return err
			}
			child.parent = parent
			parent.content = append(parent.content, xmlContent{child: child})
			continue
		}
		start := p.pos
		for !p.eof() && p.src[p.pos] != '<' {
			p.pos++
		}
		parent.content = append(parent.content, xmlContent{text: &span{start, p.pos}})
	}
}

func (p *xmlParser) parseName() (string, error) {
	start := p.pos
	for !p.eof() {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '/' || c == '>' || c == '=' {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return "", errf(ErrHeader, "expected a name at byte %d", start)
	}
	return string(p.src[start:p.pos]), nil
}

func (p *xmlParser) parseAttr() (xmlAttr, error) {
	name, err := p.parseName()
	if err != nil {
		return xmlAttr{}, err
	}
	p.skipSpace()
	if p.eof() || p.src[p.pos] != '=' {
		return xmlAttr{}, errf(ErrHeader, "attribute %q has no value", name)
	}
	p.pos++
	p.skipSpace()
	if p.eof() || (p.src[p.pos] != '"' && p.src[p.pos] != '\'') {
		return xmlAttr{}, errf(ErrHeader, "attribute %q is not quoted", name)
	}
	quote := p.src[p.pos]
	p.pos++
	start := p.pos
	for !p.eof() && p.src[p.pos] != quote {
		p.pos++
	}
	if p.eof() {
		return xmlAttr{}, errf(ErrHeader, "unterminated attribute %q", name)
	}
	end := p.pos
	p.pos++
	raw := string(p.src[start:end])
	return xmlAttr{name: name, value: xmlUnescape(raw), rawValue: raw, quote: quote}, nil
}

func (p *xmlParser) skipSpace() {
	for !p.eof() {
		switch p.src[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}

// --- rendering -------------------------------------------------------------

func (d *xmlDoc) render() []byte {
	var buf bytes.Buffer
	buf.Grow(len(d.src))
	buf.Write(d.prefix)
	renderNode(&buf, d.root, d.src)
	buf.Write(d.suffix)
	return buf.Bytes()
}

func renderNode(buf *bytes.Buffer, n *xmlNode, src []byte) {
	if !n.subtreeDirty() {
		buf.Write(src[n.rawStart:n.rawEnd])
		return
	}
	if n.text != nil {
		// A node whose text was replaced. Keep its attributes as rendered.
		buf.WriteByte('<')
		buf.WriteString(n.name)
		renderAttrs(buf, n)
		buf.WriteByte('>')
		writeEscapedTextBuf(buf, *n.text)
		buf.WriteString("</")
		buf.WriteString(n.name)
		buf.WriteByte('>')
		return
	}
	buf.WriteByte('<')
	buf.WriteString(n.name)
	renderAttrs(buf, n)
	if len(n.content) == 0 {
		buf.WriteString("/>")
		return
	}
	buf.WriteByte('>')
	for _, c := range n.content {
		switch {
		case c.child != nil:
			renderNode(buf, c.child, src)
		case c.own != nil:
			writeEscapedTextBuf(buf, *c.own)
		case c.text != nil:
			buf.Write(src[c.text.start:c.text.end])
		}
	}
	buf.WriteString("</")
	buf.WriteString(n.name)
	buf.WriteByte('>')
}

func renderAttrs(buf *bytes.Buffer, n *xmlNode) {
	for i := range n.attrs {
		a := &n.attrs[i]
		buf.WriteByte(' ')
		buf.WriteString(a.name)
		buf.WriteByte('=')
		quote := a.quote
		if quote == 0 {
			quote = '"'
		}
		buf.WriteByte(quote)
		if a.dirty {
			buf.WriteString(escapeAttrQuote(a.value, quote))
		} else {
			buf.WriteString(a.rawValue)
		}
		buf.WriteByte(quote)
	}
}

// --- text helpers ----------------------------------------------------------

func xmlUnescape(s string) string {
	if !strings.ContainsRune(s, '&') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := strings.IndexByte(s[i:], ';')
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		ent := s[i+1 : i+j]
		switch ent {
		case "amp":
			b.WriteByte('&')
		case "lt":
			b.WriteByte('<')
		case "gt":
			b.WriteByte('>')
		case "quot":
			b.WriteByte('"')
		case "apos":
			b.WriteByte('\'')
		default:
			if strings.HasPrefix(ent, "#x") || strings.HasPrefix(ent, "#X") {
				if v, err := strconv.ParseUint(ent[2:], 16, 32); err == nil {
					b.WriteRune(rune(v))
				} else {
					b.WriteString(s[i : i+j+1])
				}
			} else if strings.HasPrefix(ent, "#") {
				if v, err := strconv.ParseUint(ent[1:], 10, 32); err == nil {
					b.WriteRune(rune(v))
				} else {
					b.WriteString(s[i : i+j+1])
				}
			} else {
				b.WriteString(s[i : i+j+1])
			}
		}
		i += j + 1
	}
	return b.String()
}

func escapeAttr(s string) string { return escapeAttrQuote(s, '"') }

func escapeAttrQuote(s string, quote byte) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			if quote == '"' {
				b.WriteString("&quot;")
			} else {
				b.WriteRune(r)
			}
		case '\'':
			if quote == '\'' {
				b.WriteString("&apos;")
			} else {
				b.WriteRune(r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func writeEscapedText(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
}

func writeEscapedTextBuf(buf *bytes.Buffer, s string) {
	var b strings.Builder
	writeEscapedText(&b, s)
	buf.WriteString(b.String())
}

// validUTF8 reports whether b is valid UTF-8. It exists because XML requires
// it and a malformed header should be reported rather than half-parsed.
func validUTF8(b []byte) bool { return utf8.Valid(b) }
