package data

import "testing"

func TestParseFeed(t *testing.T) {
	rss := `<?xml version="1.0" encoding="UTF-8"?><rss><channel>
<item><title><![CDATA[央行<b>降准</b>]]></title><link>https://x.cn/a/1</link><description>&lt;p&gt;正文&lt;/p&gt;</description>
<pubDate>Wed, 07 Oct 2026 08:00:00 +0000</pubDate><guid>g-1</guid></item></channel></rss>`
	items, err := parseFeed([]byte(rss))
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	it := items[0]
	if it.Title != "央行降准" || it.Content != "正文" || it.PublishTime != "2026-10-07T16:00:00+08:00" || it.SourceID != hashID("g-1") {
		t.Fatalf("bad item %+v", it)
	}

	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><entry><title>A</title><id>urn:1</id>
<updated>2026-10-07T08:00:00Z</updated><link href="https://x.cn/b"/><summary>S</summary></entry></feed>`
	items, err = parseFeed([]byte(atom))
	if err != nil || len(items) != 1 || items[0].URL != "https://x.cn/b" || items[0].PublishTime != "2026-10-07T16:00:00+08:00" {
		t.Fatalf("atom items=%+v err=%v", items, err)
	}
}

func TestShTime(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-08 22:03:34:367": "2026-10-08T22:03:34+08:00",
		"2026-10-08 14:50:12":     "2026-10-08T14:50:12+08:00",
		"2026-10-08 00:00:00.000": "2026-10-08T00:00:00+08:00",
	} {
		if got := shTime(in); got != want {
			t.Fatalf("shTime(%q) = %q, want %q", in, got, want)
		}
	}
}
