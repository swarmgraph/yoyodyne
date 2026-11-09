package factory

import (
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The hidden line the harness ends everything it writes on the forge with: a
// pull request description, a comment, or a review. It is an HTML comment, so
// the forge does not show it to a reader, and it names the factory that wrote
// it. Reading feedback on a pull request ignores what carries a mark naming its
// own product, because that is a factory's words and not a person's, whichever
// forge account wrote them (docs/designs/working-alongside-other-contributors.md,
// section 2). The mark protects nothing but the harness from its own words: a
// person who copies the line into a comment of their own has that comment
// ignored.

const markPrefix = "<!-- yoyodyne: "

// markPattern finds a mark anywhere in a text and the product it names.
var markPattern = regexp.MustCompile(`<!--\s*yoyodyne:\s*product\s+([a-z][a-z0-9]*(?:-[a-z0-9]+)*)\b[^>]*-->`)

// Mark is the hidden line naming this identity. Every part that is known is
// named; a part that is empty, as it is where the machine or the forge account
// could not be read, is left out, and the product is always named, because the
// product is what reading feedback tests. Nothing in a part can close the
// comment early.
func (i Identity) Mark() string {
	parts := []string{"product " + markPart(i.Product)}
	if machine := markPart(i.Machine); machine != "" {
		parts = append(parts, "machine "+machine)
	}
	if account := markPart(i.ForgeAccount); account != "" {
		parts = append(parts, "forge account "+account)
	}
	return markPrefix + strings.Join(parts, ", ") + " -->"
}

// ProductMark is the mark of a factory of product whose machine and forge
// account are not known to the writer.
func ProductMark(product domain.ProductID) string {
	return Identity{Product: string(product)}.Mark()
}

// MarkedProducts is every product a mark in text names, in the order they
// appear.
func MarkedProducts(text string) []string {
	var products []string
	for _, match := range markPattern.FindAllStringSubmatch(text, -1) {
		products = append(products, match[1])
	}
	return products
}

// MarkedBy reports text carrying a mark naming product: something a factory of
// that product wrote, on this machine or another.
func MarkedBy(text string, product domain.ProductID) bool {
	if strings.TrimSpace(string(product)) == "" {
		return false
	}
	for _, named := range MarkedProducts(text) {
		if named == string(product) {
			return true
		}
	}
	return false
}

// IsMark reports a line that is a whole mark naming a product.
func IsMark(line string) bool {
	trimmed := strings.TrimSpace(line)
	match := markPattern.FindStringIndex(trimmed)
	return match != nil && match[0] == 0 && match[1] == len(trimmed) && !strings.Contains(trimmed, "\n")
}

// markPart is one part of an identity as a mark carries it: on one line, with
// nothing that would end the comment or start another.
func markPart(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	for _, closing := range []string{"-->", "--", "<", ">", ","} {
		value = strings.ReplaceAll(value, closing, "")
	}
	return strings.TrimSpace(value)
}
