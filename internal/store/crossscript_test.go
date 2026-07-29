package store

import "testing"

func sr(content string) SearchResult {
	r := SearchResult{}
	r.Content = content
	return r
}

func TestCrossScriptQuery(t *testing.T) {
	enDocs := []SearchResult{sr("Mora Iced Creamery is permanently closed."), sr("Island Cool is open daily."), sr("Delta departs Terminal 2.")}
	zhDocs := []SearchResult{sr("海芋四五天澆一次水。"), sr("備用鑰匙在陶瓷青蛙裡。"), sr("我們家不吃牛肉。")}
	mixed := append(append([]SearchResult{}, enDocs...), zhDocs[0])

	cases := []struct {
		q      string
		window []SearchResult
		want   bool
	}{
		{"冰淇淋 Bainbridge 推薦", enDocs, true},         // CJK query, EN docs → skip
		{"where is the spare key", zhDocs, true},     // EN query, CJK docs → skip
		{"ice cream recommendations", enDocs, false}, // same script → CE stays
		{"備用鑰匙放在哪裡", zhDocs, false},                  // same script → CE stays
		{"冰淇淋推薦", mixed, true},                       // majority mismatch → skip
		{"anything", nil, false},
	}
	for _, c := range cases {
		if got := crossScriptQuery(c.q, c.window); got != c.want {
			t.Errorf("crossScriptQuery(%q, %d docs) = %v, want %v", c.q, len(c.window), got, c.want)
		}
	}
	// Latin brand names inside a CJK query must not flip the dominance call.
	if !crossScriptQuery("宿舍快煮鍋 Kestrel 買哪台", enDocs) {
		t.Errorf("CJK-dominant query with embedded Latin name should still skip over EN docs")
	}
}
