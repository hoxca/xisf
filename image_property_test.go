package xisf

import (
	"bytes"
	"errors"
	"testing"
)

func TestAddImageProperty(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	const id = "PCL:OverridableIdentifier"
	if err := im.AddProperty(Property{ID: id, Type: "Boolean", Value: "false"}); err != nil {
		t.Fatalf("add image property: %v", err)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want := `<Property id="PCL:OverridableIdentifier" type="Boolean" value="false"/>`
	propPos := bytes.Index(out, []byte(want))
	imageStart := bytes.Index(out, []byte("<Image"))
	imageEnd := bytes.Index(out, []byte("</Image>"))
	if propPos < 0 || imageStart < 0 || imageEnd < 0 || propPos < imageStart || propPos > imageEnd {
		t.Fatalf("property is not inside the Image element: %s", out)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	im2 := f2.Images()[0]
	p, ok := im2.Property(id)
	if !ok || p.Type != "Boolean" || p.Value != "false" {
		t.Fatalf("image property = %+v, %v", p, ok)
	}
	if im2.Dirty() {
		t.Fatal("adding a property marked the image pixels dirty")
	}
	px, err := Pixels[uint8](im2)
	if err != nil {
		t.Fatalf("pixels: %v", err)
	}
	if !bytes.Equal(px, []byte{1, 2, 3, 4}) {
		t.Fatalf("pixels = %v", px)
	}
	for _, mp := range f2.Metadata() {
		if mp.ID == id {
			t.Fatal("image property leaked into unit metadata")
		}
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

func TestImagePropertiesAreScoped(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	if err := im.AddProperty(Property{ID: "Test:Count", Type: "UInt32", Value: "42"}); err != nil {
		t.Fatalf("add scalar: %v", err)
	}
	if err := im.AddProperty(Property{ID: "Test:Text", Type: "String", Value: "a & b"}); err != nil {
		t.Fatalf("add string: %v", err)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	im2 := f2.Images()[0]
	if got := len(im2.Properties()); got != 2 {
		t.Fatalf("image has %d properties, want 2", got)
	}
	if p, ok := im2.Property("Test:Text"); !ok || p.Value != "a & b" {
		t.Fatalf("string property = %+v, %v", p, ok)
	}
	if _, ok := im2.Property("XISF:CreatorApplication"); ok {
		t.Fatal("unit metadata property is visible as an image property")
	}
	if got := len(f2.Metadata()); got != 2 {
		t.Fatalf("unit metadata has %d properties, want 2", got)
	}
}

func TestAddImagePropertyDuplicate(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	p := Property{ID: "Test:Duplicate", Type: "UInt32", Value: "1"}
	if err := im.AddProperty(p); err != nil {
		t.Fatalf("add image property: %v", err)
	}
	before, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := im.AddProperty(p); !errors.Is(err, ErrProperty) {
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

func TestSetImageProperty(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	const id = "Test:Value"
	if err := im.AddProperty(Property{ID: id, Type: "String", Value: "one"}); err != nil {
		t.Fatalf("add image property: %v", err)
	}
	if err := im.SetProperty(Property{ID: id, Type: "String", Value: "two"}); err != nil {
		t.Fatalf("update image property: %v", err)
	}
	if err := im.SetProperty(Property{ID: "Test:Added", Type: "Boolean", Value: "true"}); err != nil {
		t.Fatalf("set missing image property: %v", err)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if bytes.Contains(out, []byte(">one<")) {
		t.Fatal("old image property value is still present")
	}
	if got := bytes.Count(out, []byte(`id="`+id+`"`)); got != 1 {
		t.Fatalf("updated property appears %d times", got)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	im2 := f2.Images()[0]
	if p, ok := im2.Property(id); !ok || p.Value != "two" {
		t.Fatalf("updated property = %+v, %v", p, ok)
	}
	if p, ok := im2.Property("Test:Added"); !ok || p.Type != "Boolean" || p.Value != "true" {
		t.Fatalf("added property = %+v, %v", p, ok)
	}
}

func TestSetImagePropertyChangesType(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	const id = "Test:Value"
	if err := im.AddProperty(Property{ID: id, Type: "String", Value: "old"}); err != nil {
		t.Fatalf("add image property: %v", err)
	}
	if err := im.SetProperty(Property{ID: id, Type: "Boolean", Value: "true"}); err != nil {
		t.Fatalf("update image property: %v", err)
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
}

func TestImagePropertyStringEscaping(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	value := `a & b < c > d "quoted"`
	if err := f.Images()[0].AddProperty(Property{
		ID:    "Test:Text",
		Type:  "String",
		Value: value,
	}); err != nil {
		t.Fatalf("add image property: %v", err)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want := `<Property id="Test:Text" type="String">a &amp; b &lt; c &gt; d "quoted"</Property>`
	if !bytes.Contains(out, []byte(want)) {
		t.Fatalf("written header does not contain %s", want)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if p, ok := f2.Images()[0].Property("Test:Text"); !ok || p.Value != value {
		t.Fatalf("string property = %+v, %v", p, ok)
	}
}

func TestImagePropertyRejectsDataBlocks(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	p := Property{
		ID:       "Test:Payload",
		Type:     "ByteArray",
		Location: &Location{Kind: LocEmbedded},
	}
	if err := im.AddProperty(p); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("AddProperty error = %v, want ErrUnsupported", err)
	}
	if err := im.SetProperty(p); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SetProperty error = %v, want ErrUnsupported", err)
	}
	if _, ok := im.Property("Test:Payload"); ok {
		t.Fatal("rejected property was added")
	}
}

func TestImagePropertyValidation(t *testing.T) {
	f, err := Parse(buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	for _, p := range []Property{
		{Type: "Boolean", Value: "false"},
		{ID: "Test:NoType"},
	} {
		if err := im.AddProperty(p); !errors.Is(err, ErrProperty) {
			t.Fatalf("AddProperty(%+v) error = %v, want ErrProperty", p, err)
		}
		if err := im.SetProperty(p); !errors.Is(err, ErrProperty) {
			t.Fatalf("SetProperty(%+v) error = %v, want ErrProperty", p, err)
		}
	}
}
