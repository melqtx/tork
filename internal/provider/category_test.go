package provider

import "testing"

func TestCategoryNormalization(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"TV/Anime", "anime"}, {"Anime > TV", "anime"}, {"Movies/HD", "movies"},
		{"Movies/Books", ""}, {"Books/Movies", ""}, {"XXX/Movies", ""},
		{"Spiderman.2002.BluRay.mkv", ""}, {"Other", ""}, {" E-books ", "books"},
	} {
		if got := NormalizeCategory(tc.input); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMergedCategoryConflictCannotBeOverwritten(t *testing.T) {
	for _, categories := range [][]string{{"Movies", "Books", "Movies"}, {"Books", "Movies", "Books"}} {
		result := Result{Category: categories[0]}
		for _, category := range categories[1:] {
			result = Merge(result, Result{Category: category})
		}
		if !result.CategoryConflict || result.Category != "" {
			t.Fatalf("conflicting sources selected a category: %+v", result)
		}
	}
	result := Merge(Result{Category: "HD"}, Result{Category: "Movies"})
	if result.Category != "Movies" || result.CategoryConflict {
		t.Fatalf("missing category lost a known source category: %+v", result)
	}
}
