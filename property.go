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

// AddProperty appends p to the unit's <Metadata> element. It returns
// ErrProperty if a property with the same identifier already exists in
// Metadata. Properties whose value is stored in a data block are rejected
// with ErrUnsupported.
func (f *File) AddProperty(p Property) error {
	if err := validateProperty(p); err != nil {
		return err
	}
	meta := f.metadataElement(true)
	if findProperty(meta, p.ID) != nil {
		return errf(ErrProperty, "property %q already exists", p.ID)
	}
	meta.appendChild(newPropertyNode(p))
	f.metaDirty = true
	return nil
}

// SetProperty adds p to the unit's <Metadata> element, or updates the existing
// Metadata property with the same identifier. Updating a property that stores
// its value in a data block is not supported.
func (f *File) SetProperty(p Property) error {
	if err := validateProperty(p); err != nil {
		return err
	}
	meta := f.metadataElement(true)
	if n := findProperty(meta, p.ID); n != nil {
		if _, ok := n.attrValue("location"); ok {
			return errf(ErrUnsupported, "property %q stores its value in a data block", p.ID)
		}
		updatePropertyNode(n, p)
	} else {
		meta.appendChild(newPropertyNode(p))
	}
	f.metaDirty = true
	return nil
}

func validateProperty(p Property) error {
	if p.ID == "" {
		return errf(ErrProperty, "property id is empty")
	}
	if p.Type == "" {
		return errf(ErrProperty, "property %q has no type", p.ID)
	}
	if p.Location != nil {
		return errf(ErrUnsupported, "property %q uses a data block location", p.ID)
	}
	return nil
}

// metadataElement returns the unit's <Metadata> element, creating it as the
// last child of the root when create is true and it does not exist.
func (f *File) metadataElement(create bool) *xmlNode {
	for _, n := range f.doc.root.children() {
		if n.localName() == "Metadata" {
			return n
		}
	}
	if !create {
		return nil
	}
	n := &xmlNode{name: "Metadata", dirty: true}
	f.doc.root.appendChild(n)
	return n
}

func findProperty(meta *xmlNode, id string) *xmlNode {
	if meta == nil {
		return nil
	}
	for _, n := range meta.children() {
		if n.localName() != "Property" {
			continue
		}
		if pid, _ := n.attrValue("id"); pid == id {
			return n
		}
	}
	return nil
}

func newPropertyNode(p Property) *xmlNode {
	n := &xmlNode{name: "Property", dirty: true}
	n.setAttr("id", p.ID)
	n.setAttr("type", p.Type)
	setOptionalPropertyAttr(n, "format", p.Format)
	setOptionalPropertyAttr(n, "comment", p.Comment)
	setOptionalPropertyInt(n, "length", p.Length, p.HasLength)
	setOptionalPropertyInt(n, "rows", p.Rows, p.HasRows)
	setOptionalPropertyInt(n, "columns", p.Columns, p.HasColumns)
	if p.Type == "String" {
		n.setText(p.Value)
	} else {
		n.setAttr("value", p.Value)
	}
	return n
}

func updatePropertyNode(n *xmlNode, p Property) {
	n.setAttr("id", p.ID)
	n.setAttr("type", p.Type)
	setOptionalPropertyAttr(n, "format", p.Format)
	setOptionalPropertyAttr(n, "comment", p.Comment)
	setOptionalPropertyInt(n, "length", p.Length, p.HasLength)
	setOptionalPropertyInt(n, "rows", p.Rows, p.HasRows)
	setOptionalPropertyInt(n, "columns", p.Columns, p.HasColumns)
	if p.Type == "String" {
		n.removeAttr("value")
		n.setText(p.Value)
		return
	}
	n.setAttr("value", p.Value)
	// Scalar, complex and time point properties are empty elements. Drop any
	// character data left over from a previous inline-string value.
	n.text = nil
	n.content = nil
	n.dirty = true
}

func setOptionalPropertyAttr(n *xmlNode, name, value string) {
	if value == "" {
		n.removeAttr(name)
		return
	}
	n.setAttr(name, value)
}

func setOptionalPropertyInt(n *xmlNode, name string, value int64, present bool) {
	if !present {
		n.removeAttr(name)
		return
	}
	n.setAttr(name, strconv.FormatInt(value, 10))
}

func (f *File) alignmentFromMetadata() int {
	if p, ok := f.Property("XISF:BlockAlignmentSize"); ok && p.Value != "" {
		if v, err := strconv.Atoi(p.Value); err == nil && v > 0 {
			return v
		}
	}
	return 0
}
