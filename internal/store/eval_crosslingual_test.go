package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rcliao/ghost/internal/embedding"
)

// Cross-lingual retrieval eval — the household failure mode found live
// 2026-07-27: a fact stored in English ("Mora Iced Creamery is PERMANENTLY
// CLOSED") was reachable from an English query but NOT from the natural
// Chinese phrasing ("冰淇淋 Bainbridge 推薦"). The family chats in mixed
// zh-TW/English; the distillation tier often writes English; FTS cannot
// bridge scripts, so the vector channel is the only cross-lingual path —
// and all-MiniLM-L6-v2 is English-centric.
//
// Pairs are synthetic mirrors of real household shapes (no life data).
// Gated on GHOST_XLING_EMBED=1 (downloads/loads the local model; slower).
// A/B embedders via GHOST_EMBED_MODEL_LOCAL.
//
//	GHOST_XLING_EMBED=1 go test ./internal/store/ -run TestEvalCrossLingual -v
//	GHOST_XLING_EMBED=1 GHOST_EMBED_MODEL_LOCAL=paraphrase-multilingual-MiniLM-L12-v2 \
//	  go test ./internal/store/ -run TestEvalCrossLingual -v
type xlingCase struct {
	memory string // stored content
	query  string // other-script query that must reach it
	want   string // substring that must appear in assembled context
}

// EN-stored memory ⇐ ZH query: the live failure direction.
var xlingENMemZHQuery = []xlingCase{
	{"Solaris Creamery on Harbor Island is permanently closed — do not recommend it.", "冰淇淋店 Harbor Island 推薦", "Solaris Creamery"},
	{"The dorm electric pot decision: Kestrel K-11, 304 stainless, detachable base.", "宿舍快煮鍋最後買了哪一台", "Kestrel"},
	{"Family rule: no beef at family meals.", "家裡吃飯有什麼忌口的嗎", "no beef"},
	{"The spare house key lives inside the blue ceramic frog by the front door.", "備用鑰匙放在哪裡", "ceramic frog"},
	{"Xyzal takes about 40 minutes to relieve her hives.", "過敏藥多久會生效", "Xyzal"},
	{"The calla lily gets watered only when topsoil is dry, roughly every 4-5 days.", "海芋多久澆一次水", "calla"},
	{"Delta departs from Terminal 2 at the regional airport.", "航廈是哪一個 Delta", "Terminal 2"},
	{"Grandma's birthday dinner is booked at the harbor-view restaurant for the 15th.", "奶奶生日晚餐訂在哪裡", "harbor-view"},
	{"The blue-gray neighborhood cat visits the backyard most mornings.", "那隻藍灰色的貓通常什麼時候來", "blue-gray"},
	{"Preferred sunscreen: mineral only, reapply every two hours outdoors.", "防曬要注意什麼", "mineral"},
}

// ZH-stored memory ⇐ EN query: the reverse direction.
var xlingZHMemENQuery = []xlingCase{
	{"我們家吃飯都不吃牛肉。", "what meat does the family avoid", "牛肉"},
	{"備用鑰匙藏在前門旁邊的藍色陶瓷青蛙裡面。", "where is the spare key hidden", "青蛙"},
	{"宿舍快煮鍋決定買 Kestrel K-11，304不鏽鋼、分離式底座。", "which electric pot did we pick for the dorm", "Kestrel"},
	{"海芋大約四到五天澆一次水，表土乾了才澆。", "how often to water the calla lily", "海芋"},
	{"過敏藥大約四十分鐘後開始止癢。", "how long until the allergy medicine works", "四十分鐘"},
	{"奶奶生日晚餐訂在十五號的海景餐廳。", "where is grandma's birthday dinner booked", "海景"},
	{"這款吸塵器的濾網每兩個月要洗一次。", "how often to clean the vacuum filter", "濾網"},
	{"新的路由器密碼貼在書房抽屜裡面。", "where did we put the new router password", "抽屜"},
	{"小朋友的鋼琴課改到每週三下午四點。", "when is the piano lesson now", "週三"},
	{"這家修車廠只收現金，記得先領錢。", "anything to know before the car repair shop", "現金"},
}

func runXling(t *testing.T, s *SQLiteStore, cases []xlingCase, label string) (hits int) {
	ctx := context.Background()
	ns := "agent:xling-" + strings.ToLower(strings.ReplaceAll(label, " ", "-"))
	for i, c := range cases {
		if _, err := s.Put(ctx, PutParams{NS: ns, Key: fmt.Sprintf("fact-%d", i),
			Content: c.memory, Kind: "semantic", Tier: "stm", Importance: 0.6}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	// Distractors: plausible same-domain noise so the probe is not a gimme.
	for i, d := range []string{
		"Weekend market runs Saturdays at the plaza.",
		"The garage light switch sticks sometimes.",
		"新的購物袋放在車庫架子上。",
		"圖書館週一休館。",
	} {
		s.Put(ctx, PutParams{NS: ns, Key: fmt.Sprintf("noise-%d", i), Content: d,
			Kind: "semantic", Tier: "stm", Importance: 0.5})
	}
	for _, c := range cases {
		res, err := s.Context(ctx, ContextParams{NS: ns, Query: c.query,
			Budget: 800, MinScore: 0.3, ExcludePinned: true})
		if err != nil {
			t.Fatalf("context %q: %v", c.query, err)
		}
		var joined strings.Builder
		for _, m := range res.Memories {
			joined.WriteString(m.Content + "\n")
		}
		if strings.Contains(joined.String(), c.want) {
			hits++
		} else {
			t.Logf("  MISS [%s] q=%q want=%q", label, c.query, c.want)
		}
	}
	return hits
}

func TestEvalCrossLingual(t *testing.T) {
	if os.Getenv("GHOST_XLING_EMBED") != "1" {
		t.Skip("set GHOST_XLING_EMBED=1 to run (loads local embedding model)")
	}
	s := newTestStore(t)
	emb := embedding.NewLocalEmbedder()
	if emb == nil {
		t.Skip("local embedder unavailable")
	}
	s.SetEmbedder(emb)

	en := runXling(t, s, xlingENMemZHQuery, "en-mem zh-query")
	zh := runXling(t, s, xlingZHMemENQuery, "zh-mem en-query")
	model := os.Getenv("GHOST_EMBED_MODEL_LOCAL")
	if model == "" {
		model = "all-MiniLM-L6-v2"
	}
	t.Logf("cross-lingual recall model=%s: en-mem/zh-query %d/%d, zh-mem/en-query %d/%d, TOTAL %d/%d",
		model, en, len(xlingENMemZHQuery), zh, len(xlingZHMemENQuery),
		en+zh, len(xlingENMemZHQuery)+len(xlingZHMemENQuery))
}
