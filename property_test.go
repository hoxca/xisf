package xisf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// headerOnlyFile builds a valid monolithic unit with no images and no
// Metadata element. It is used to exercise metadata creation.
func headerOnlyFile(t *testing.T) []byte {
	t.Helper()
	header := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf"></xisf>`
	out := make([]byte, PreambleLen+len(header))
	copy(out, Signature)
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(header)))
	copy(out[PreambleLen:], header)
	return out
}

func TestAddProperty(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	const id = "PCL:OverridableIdentifier"
	if err := f.AddProperty(Property{ID: id, Type: "Boolean", Value: "false"}); err != nil {
		t.Fatalf("add property: %v", err)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want := `<Property id="PCL:OverridableIdentifier" type="Boolean" value="false"/>`
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("written header does not contain %s", want)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	p, ok := f2.Property(id)
	if !ok {
		t.Fatalf("property %q missing after round trip", id)
	}
	if p.Type != "Boolean" || p.Value != "false" {
		t.Fatalf("property = %+v", p)
	}
	if _, ok := f2.Property("XISF:CreatorApplication"); !ok {
		t.Fatal("existing metadata property was lost")
	}
	px, err := Pixels[uint8](f2.Images()[0])
	if err != nil {
		t.Fatalf("pixels: %v", err)
	}
	if !bytes.Equal(px, []byte{1, 2, 3, 4}) {
		t.Fatalf("pixels = %v", px)
	}

	// A second write must not append the property again.
	again, err := f2.Bytes(nil)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if got := bytes.Count(again, []byte(`id="`+id+`"`)); got != 1 {
		t.Fatalf("property appears %d times", got)
	}
}

func TestAddPropertyCreatesMetadata(t *testing.T) {
	f, err := Parse(headerOnlyFile(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(f.Metadata()); got != 0 {
		t.Fatalf("fixture has %d metadata properties", got)
	}
	if err := f.AddProperty(Property{ID: "Test:Created", Type: "String", Value: "yes"}); err != nil {
		t.Fatalf("add property: %v", err)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want := `<Metadata><Property id="Test:Created" type="String">yes</Property></Metadata>`
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("written header does not contain %s", want)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := len(f2.Metadata()); got != 1 {
		t.Fatalf("metadata has %d properties", got)
	}
	p, ok := f2.Property("Test:Created")
	if !ok || p.Type != "String" || p.Value != "yes" {
		t.Fatalf("property = %+v, %v", p, ok)
	}
}

func TestAddPropertyDuplicate(t *testing.T) {
	f, err := Parse(headerOnlyFile(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := Property{ID: "Test:Duplicate", Type: "UInt32", Value: "1"}
	if err := f.AddProperty(p); err != nil {
		t.Fatalf("add property: %v", err)
	}
	before, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.AddProperty(p); !errors.Is(err, ErrProperty) {
		t.Fatalf("duplicate error = %v, want ErrProperty", err)
	}
	after, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write after duplicate: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed AddProperty changed the file")
	}
}

func TestAddPropertyStringEscaping(t *testing.T) {
	f, err := Parse(headerOnlyFile(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	value := `a & b < c > d "quoted"`
	if err := f.AddProperty(Property{ID: "Test:Text", Type: "String", Value: value}); err != nil {
		t.Fatalf("add property: %v", err)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want := `<Property id="Test:Text" type="String">a &amp; b &lt; c &gt; d "quoted"</Property>`
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("written header does not contain %s", want)
	}
	if bytes.Contains(out, []byte(`id="Test:Text" type="String" value=`)) {
		t.Fatal("String property was written with a value attribute")
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	p, ok := f2.Property("Test:Text")
	if !ok || p.Value != value {
		t.Fatalf("property = %+v, %v", p, ok)
	}
}

func TestAddPropertyOptionalAttributes(t *testing.T) {
	f, err := Parse(headerOnlyFile(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := Property{
		ID:      "Test:Flag",
		Type:    "Boolean",
		Value:   "true",
		Format:  "width:5",
		Comment: "a flag",
	}
	if err := f.AddProperty(p); err != nil {
		t.Fatalf("add property: %v", err)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want := `<Property id="Test:Flag" type="Boolean" format="width:5" comment="a flag" value="true"/>`
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("written header does not contain %s", want)
	}
}

func TestSetPropertyUpdatesExisting(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := f.SetProperty(Property{
		ID:    "XISF:CreatorApplication",
		Type:  "String",
		Value: "xisf-test-v2",
	}); err != nil {
		t.Fatalf("set property: %v", err)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if bytes.Contains(out, []byte(">xisf-test<")) {
		t.Fatal("old property value is still present")
	}
	if got := bytes.Count(out, []byte(`id="XISF:CreatorApplication"`)); got != 1 {
		t.Fatalf("property appears %d times", got)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	p, ok := f2.Property("XISF:CreatorApplication")
	if !ok || p.Value != "xisf-test-v2" {
		t.Fatalf("property = %+v, %v", p, ok)
	}
}

func TestSetPropertyUpdatesScalar(t *testing.T) {
	f, err := Parse(headerOnlyFile(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	const id = "Test:Count"
	if err := f.AddProperty(Property{ID: id, Type: "UInt32", Value: "1"}); err != nil {
		t.Fatalf("add property: %v", err)
	}
	if err := f.SetProperty(Property{ID: id, Type: "UInt32", Value: "2"}); err != nil {
		t.Fatalf("set property: %v", err)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Contains(out, []byte(`value="2"`)) || bytes.Contains(out, []byte(`value="1"`)) {
		t.Fatalf("scalar was not updated: %s", out)
	}
	if got := bytes.Count(out, []byte(`id="Test:Count"`)); got != 1 {
		t.Fatalf("property appears %d times", got)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	p, ok := f2.Property(id)
	if !ok || p.Type != "UInt32" || p.Value != "2" || p.HasLength {
		t.Fatalf("property = %+v, %v", p, ok)
	}
}

func TestSetPropertyChangesType(t *testing.T) {
	f, err := Parse(headerOnlyFile(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	const id = "Test:Value"
	if err := f.AddProperty(Property{ID: id, Type: "String", Value: "old"}); err != nil {
		t.Fatalf("add property: %v", err)
	}
	if err := f.SetProperty(Property{ID: id, Type: "Boolean", Value: "true"}); err != nil {
		t.Fatalf("set property: %v", err)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want := `<Property id="Test:Value" type="Boolean" value="true"/>`
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("written header does not contain %s", want)
	}
	if bytes.Contains(out, []byte("old")) {
		t.Fatal("old string value is still present")
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	p, ok := f2.Property(id)
	if !ok || p.Type != "Boolean" || p.Value != "true" {
		t.Fatalf("property = %+v, %v", p, ok)
	}
}

func TestPropertyRejectsDataBlocks(t *testing.T) {
	f, err := Parse(headerOnlyFile(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := Property{
		ID:       "Test:Payload",
		Type:     "ByteArray",
		Location: &Location{Kind: LocEmbedded},
	}
	if err := f.AddProperty(p); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("AddProperty error = %v, want ErrUnsupported", err)
	}
	if err := f.SetProperty(p); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetProperty error = %v, want ErrUnsupported", err)
	}
	if _, ok := f.Property("Test:Payload"); ok {
		t.Fatal("rejected property was added")
	}
}

func TestPropertyValidation(t *testing.T) {
	f, err := Parse(headerOnlyFile(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, p := range []Property{
		{Type: "Boolean", Value: "false"},
		{ID: "Test:NoType"},
	} {
		if err := f.AddProperty(p); !errors.Is(err, ErrProperty) {
			t.Fatalf("AddProperty(%+v) error = %v, want ErrProperty", p, err)
		}
		if err := f.SetProperty(p); !errors.Is(err, ErrProperty) {
			t.Fatalf("SetProperty(%+v) error = %v, want ErrProperty", p, err)
		}
	}
}
