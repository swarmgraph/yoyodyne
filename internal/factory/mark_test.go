package factory

import (
	"strings"
	"testing"
)

// The mark is one hidden line naming every part of the identity that is known,
// and reading it back finds the product, wherever in a text it is.
func TestAMarkNamesTheFactoryAndIsFoundByItsProduct(t *testing.T) {
	t.Parallel()

	whole := Identity{Product: "yoyodyne", Machine: "machine-0123456789abcdef0123456789abcdef", ForgeAccount: "factory-bot"}.Mark()
	if want := "<!-- yoyodyne: product yoyodyne, machine machine-0123456789abcdef0123456789abcdef, forge account factory-bot -->"; whole != want {
		t.Fatalf("Mark() = %q, want %q", whole, want)
	}
	if !IsMark(whole) || !IsMark(ProductMark("yoyodyne")) {
		t.Fatalf("IsMark() refused %q or the product's own mark", whole)
	}
	body := "Yoyodyne closed this pull request.\n\n" + whole
	if !MarkedBy(body, "yoyodyne") || MarkedBy(body, "other-product") || MarkedBy("no mark here", "yoyodyne") {
		t.Fatalf("MarkedBy() does not tell this product's mark from another's or from none")
	}
	if products := MarkedProducts("a\n" + ProductMark("first") + "\nb " + ProductMark("second")); strings.Join(products, ",") != "first,second" {
		t.Fatalf("MarkedProducts() = %v, want both products in order", products)
	}
	// A product named only as a prefix of another is not that product.
	if MarkedBy(ProductMark("yoyodyne-two"), "yoyodyne") {
		t.Fatal("a mark naming yoyodyne-two was read as naming yoyodyne")
	}
}

// Nothing in an identity can end the hidden comment early or put the mark on
// two lines, and a part that is not known is left out rather than named empty.
func TestAMarkCannotBeBrokenByWhatItNames(t *testing.T) {
	t.Parallel()

	mark := Identity{Product: "yoyodyne", Machine: "", ForgeAccount: "evil -->\nshown"}.Mark()
	if strings.Count(mark, "-->") != 1 || !strings.HasSuffix(mark, " -->") || strings.Contains(mark, "\n") || strings.Contains(mark, "machine") {
		t.Fatalf("Mark() = %q, want one closing, on one line, with the unknown machine left out", mark)
	}
	if !IsMark(mark) || !MarkedBy(mark, "yoyodyne") {
		t.Fatalf("Mark() = %q is not read back as this product's", mark)
	}
	for _, line := range []string{"", "<!-- yoyodyne: -->", "<!-- something else -->", "text " + ProductMark("yoyodyne")} {
		if IsMark(line) {
			t.Errorf("IsMark(%q) = true, want only a whole mark accepted", line)
		}
	}
}
