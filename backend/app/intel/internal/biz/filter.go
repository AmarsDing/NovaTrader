package biz

import (
	"strings"
	"unicode/utf8"
)

var defaultAdWords = []string{"加微信", "免费领取", "荐股", "内参", "开户送", "扫码进群", "加群", "牛股推荐"}

const minTextRunes = 6

// Filter 判断低质情报。命中时返回原因。
type Filter struct {
	blocked map[string]bool
	adWords []string
}

func NewFilter(blockedSources, adWords []string) *Filter {
	f := &Filter{blocked: map[string]bool{}, adWords: adWords}
	for _, s := range blockedSources {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			f.blocked[s] = true
		}
	}
	if len(f.adWords) == 0 {
		f.adWords = defaultAdWords
	}
	return f
}

func (f *Filter) Check(it *Item) string {
	if f.blocked[strings.ToLower(it.Source)] {
		return "来源已屏蔽：" + it.Source
	}
	text := it.Title + "\n" + it.Content
	for _, w := range f.adWords {
		if w != "" && strings.Contains(text, w) {
			return "广告词：" + w
		}
	}
	if utf8.RuneCountInString(it.Title)+utf8.RuneCountInString(it.Content) < minTextRunes {
		return "文本过短"
	}
	return ""
}
