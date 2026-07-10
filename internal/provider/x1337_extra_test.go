package provider

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// A 1337x row tagged with the XXX icon class must be flagged adult so the
// content filter catches it even though 1337x exposes no other category field.
func TestX1337IconCategoryDetectsXXX(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
<table class="table-list"><tbody>
  <tr>
    <td class="coll-1 name"><a href="/sub/8/0/" class="icon"><i class="flaticon-xxx"></i></a><a href="/torrent/1/some-pack/">Some Pack 2024</a></td>
    <td class="coll-2 seeds">3</td>
    <td class="coll-3 leeches">1</td>
    <td class="coll-4 size">1 GB</td>
  </tr>
</tbody></table>`))
	if err != nil {
		t.Fatal(err)
	}
	row := doc.Find("tbody tr").First()
	if got := iconCategory(row); got != "xxx" {
		t.Fatalf("iconCategory = %q, want xxx", got)
	}
	if !isAdultResult(Result{Title: "Some Pack 2024", Category: iconCategory(row)}) {
		t.Error("a flaticon-xxx row should be flagged adult")
	}
}
