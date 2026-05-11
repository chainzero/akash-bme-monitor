package announcements

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/chainzero/akash-bme-monitor/internal/config"
	"github.com/chainzero/akash-bme-monitor/internal/types"
)

type mockAlerter struct {
	sends []types.Alert
}

func (m *mockAlerter) Send(alert types.Alert)          { m.sends = append(m.sends, alert) }
func (m *mockAlerter) Resolve(key, title, body string) {}
func (m *mockAlerter) Post(title, body string)         {}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// --- guidToKey ---

func TestGuidToKey_AlphanumericPassthrough(t *testing.T) {
	got := guidToKey("abc123")
	if got != "abc123" {
		t.Errorf("guidToKey(%q) = %q, want %q", "abc123", got, "abc123")
	}
}

func TestGuidToKey_UppercaseLowered(t *testing.T) {
	got := guidToKey("ABC")
	if got != "abc" {
		t.Errorf("guidToKey(%q) = %q, want %q", "ABC", got, "abc")
	}
}

func TestGuidToKey_SpecialCharsReplaced(t *testing.T) {
	got := guidToKey("forum.pyth.network-topic-2393")
	want := "forum_pyth_network_topic_2393"
	if got != want {
		t.Errorf("guidToKey(%q) = %q, want %q", "forum.pyth.network-topic-2393", got, want)
	}
}

func TestGuidToKey_EmptyString(t *testing.T) {
	got := guidToKey("")
	if got != "" {
		t.Errorf("guidToKey(%q) = %q, want empty", "", got)
	}
}

func TestGuidToKey_AllSpecialChars(t *testing.T) {
	got := guidToKey("!@#$%")
	if got != "_____" {
		t.Errorf("guidToKey(%q) = %q, want _____", "!@#$%", got)
	}
}

// --- matchKeywords ---

func makeMonitor(keywords []string, srvURL string, a *mockAlerter) *PythForumMonitor {
	cfg := config.PythForumConfig{
		URL:      srvURL,
		Keywords: keywords,
	}
	return NewPythForumMonitor(cfg, a, testLogger())
}

func TestMatchKeywords_NoMatch(t *testing.T) {
	m := makeMonitor([]string{"guardian rotation"}, "", &mockAlerter{})
	item := rssItem{Title: "unrelated post", Description: "nothing to see here"}
	got := m.matchKeywords(item)
	if len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
}

func TestMatchKeywords_SingleMatch_InTitle(t *testing.T) {
	m := makeMonitor([]string{"guardian rotation"}, "", &mockAlerter{})
	item := rssItem{Title: "Proposal: Guardian Rotation for Q1", Description: "details here"}
	got := m.matchKeywords(item)
	if len(got) != 1 || got[0] != "guardian rotation" {
		t.Errorf("matchKeywords() = %v, want [guardian rotation]", got)
	}
}

func TestMatchKeywords_SingleMatch_InDescription(t *testing.T) {
	m := makeMonitor([]string{"guardian rotation"}, "", &mockAlerter{})
	item := rssItem{Title: "regular proposal", Description: "This describes a guardian rotation plan"}
	got := m.matchKeywords(item)
	if len(got) != 1 {
		t.Errorf("expected 1 match in description, got %v", got)
	}
}

func TestMatchKeywords_CaseInsensitive(t *testing.T) {
	m := makeMonitor([]string{"GUARDIAN"}, "", &mockAlerter{})
	item := rssItem{Title: "guardian set update", Description: ""}
	got := m.matchKeywords(item)
	if len(got) != 1 {
		t.Errorf("expected case-insensitive match, got %v", got)
	}
}

func TestMatchKeywords_MultipleKeywords_AllMatch(t *testing.T) {
	m := makeMonitor([]string{"guardian", "wormhole", "rotation"}, "", &mockAlerter{})
	item := rssItem{Title: "wormhole guardian rotation proposal", Description: ""}
	got := m.matchKeywords(item)
	if len(got) != 3 {
		t.Errorf("expected 3 matches, got %v", got)
	}
}

func TestMatchKeywords_EmptyKeywords(t *testing.T) {
	m := makeMonitor([]string{}, "", &mockAlerter{})
	item := rssItem{Title: "any post", Description: "any description"}
	got := m.matchKeywords(item)
	if len(got) != 0 {
		t.Errorf("expected no matches with empty keywords, got %v", got)
	}
}

// --- fetchFeed ---

func rssBody(items ...string) string {
	itemsXML := ""
	for _, item := range items {
		itemsXML += item
	}
	return fmt.Sprintf(`<?xml version="1.0"?><rss><channel>%s</channel></rss>`, itemsXML)
}

func rssItem_(guid, title, desc string) string {
	return fmt.Sprintf("<item><guid>%s</guid><title>%s</title><description>%s</description><link>http://example.com/%s</link></item>", guid, title, desc, guid)
}

func TestFetchFeed_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssBody(rssItem_("1", "Post One", "body one")))
	}))
	defer srv.Close()

	m := makeMonitor(nil, srv.URL, &mockAlerter{})
	feed, err := m.fetchFeed(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(feed.Channel.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(feed.Channel.Items))
	}
}

func TestFetchFeed_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := makeMonitor(nil, srv.URL, &mockAlerter{})
	_, err := m.fetchFeed(context.Background())
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestFetchFeed_InvalidXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not xml at all")
	}))
	defer srv.Close()

	m := makeMonitor(nil, srv.URL, &mockAlerter{})
	_, err := m.fetchFeed(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
}

func TestFetchFeed_Unreachable(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close()

	m := makeMonitor(nil, url, &mockAlerter{})
	_, err := m.fetchFeed(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

// --- check() ---

func TestCheck_Baseline_NoAlerts(t *testing.T) {
	// First call is a baseline — items are recorded but no alerts sent.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssBody(rssItem_("guid1", "Guardian Rotation Post", "wormhole")))
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := makeMonitor([]string{"guardian"}, srv.URL, a)
	m.check(context.Background())

	if len(a.sends) != 0 {
		t.Errorf("expected no alerts on baseline pass, got %d", len(a.sends))
	}
	if !m.baselined {
		t.Error("expected baselined = true after first check")
	}
}

func TestCheck_NewItem_MatchingKeyword_AlertSent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssBody(rssItem_("guid1", "Old Post", "old stuff")))
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := makeMonitor([]string{"guardian"}, srv.URL, a)
	m.check(context.Background()) // baseline with guid1

	// Now serve a new item with a matching keyword.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssBody(
			rssItem_("guid1", "Old Post", "old stuff"),
			rssItem_("guid2", "Guardian Rotation Update", "important wormhole news"),
		))
	})
	m.check(context.Background())

	if len(a.sends) != 1 {
		t.Errorf("expected 1 alert for new matching item, got %d", len(a.sends))
	}
}

func TestCheck_NewItem_NoKeywordMatch_NoAlert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssBody())
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := makeMonitor([]string{"guardian"}, srv.URL, a)
	m.check(context.Background()) // baseline (empty)

	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssBody(rssItem_("guid1", "Unrelated topic", "nothing here")))
	})
	m.check(context.Background())

	if len(a.sends) != 0 {
		t.Errorf("expected no alert for non-matching item, got %d", len(a.sends))
	}
}

func TestCheck_SameItemNotAlertedTwice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssBody(rssItem_("guid1", "Old Post", "not relevant")))
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := makeMonitor([]string{"guardian"}, srv.URL, a)
	m.check(context.Background()) // baseline

	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, rssBody(rssItem_("guid2", "Guardian Rotation", "big news")))
	})
	m.check(context.Background()) // sends 1 alert for guid2

	// Same feed again — guid2 already seen.
	m.check(context.Background()) // should not re-alert

	if len(a.sends) != 1 {
		t.Errorf("expected 1 total alert (no duplicate), got %d", len(a.sends))
	}
}
