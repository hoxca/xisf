package xisf

import "strconv"

// Property is one <Property> core element. Values are exposed as text; the
// raw XML is preserved untouched on write.
type Property struct {
	// ID is the property identifier, such as "XISF:CreationTime".
	ID string
	// Type is the declared XISF type name.
	Type string
	// Value is the text or value attribute, decoded.
	Value string
	// Format is the optional format hint for vector and matrix types.
	Format string
	// Comment is the optional human-readable comment.
	Comment string

	// Length, Rows and Columns are set when the corresponding attribute is
	// present. Has* reports presence, since zero is a valid value.
	Length     int64
	Rows       int64
	Columns    int64
	HasLength  bool
	HasRows    bool
	HasColumns bool

	// Location is the parsed data block location, when the property stores
	// its payload in a data block rather than inline.
	Location *Location

	elem *xmlNode
}

func parseProperty(n *xmlNode, src []byte) *Property {
	p := &Property{elem: n}
	p.ID, _ = n.attrValue("id")
	p.Type, _ = n.attrValue("type")
	p.Format, _ = n.attrValue("format")
	p.Comment, _ = n.attrValue("comment")
	if v, ok := n.attrValue("value"); ok {
		p.Value = v
	} else {
		p.Value = n.textValue(src)
	}
	if v, ok := n.attrValue("length"); ok {
		p.Length, _ = strconv.ParseInt(v, 10, 64)
		p.HasLength = true
	}
	if v, ok := n.attrValue("rows"); ok {
		p.Rows, _ = strconv.ParseInt(v, 10, 64)
		p.HasRows = true
	}
	if v, ok := n.attrValue("columns"); ok {
		p.Columns, _ = strconv.ParseInt(v, 10, 64)
		p.HasColumns = true
	}
	if v, ok := n.attrValue("location"); ok {
		if loc, err := parseLocation(v); err == nil {
			p.Location = &loc
		}
	}
	return p
}

// Metadata returns the properties of the unit's <Metadata> element.
func (f *File) Metadata() []*Property {
	var out []*Property
	for _, n := range f.doc.root.children() {
		if n.localName() != "Metadata" {
			continue
		}
		for _, c := range n.children() {
			if c.localName() == "Property" {
				out = append(out, parseProperty(c, f.doc.src))
			}
		}
	}
	return out
}

// Properties returns every property in the header, wherever it appears.
func (f *File) Properties() []*Property {
	var out []*Property
	var walk func(n *xmlNode)
	walk = func(n *xmlNode) {
		if n.localName() == "Property" {
			out = append(out, parseProperty(n, f.doc.src))
		}
		for _, c := range n.children() {
			walk(c)
		}
	}
	walk(f.doc.root)
	return out
}

// Property returns the first property with the given identifier.
func (f *File) Property(id string) (*Property, bool) {
	for _, p := range f.Properties() {
		if p.ID == id {
			return p, true
		}
	}
	return nil, false
}

func (f *File) alignmentFromMetadata() int {
	if p, ok := f.Property("XISF:BlockAlignmentSize"); ok && p.Value != "" {
		if v, err := strconv.Atoi(p.Value); err == nil && v > 0 {
			return v
		}
	}
	return 0
}
