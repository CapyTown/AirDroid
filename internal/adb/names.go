package adb

import (
	"strings"
	"unicode"
)

// addMarketName records a name the device is sold by, once.
func (i *Info) addMarketName(name string) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, i.Model) {
		return
	}
	for _, n := range i.MarketNames {
		if n == name {
			return
		}
	}
	i.MarketNames = append(i.MarketNames, name)
	if i.MarketName == "" {
		i.MarketName = name
	}
}

// hasHan reports Chinese characters in s.
func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// brands are makers' names in English and in Chinese, as they write them.
var brands = []struct{ en, zh string }{
	{"OnePlus", "一加"},
	{"Xiaomi", "小米"},
	{"HUAWEI", "华为"},
	{"HONOR", "荣耀"},
	{"realme", "真我"},
	{"Meizu", "魅族"},
	{"nubia", "努比亚"},
	{"ZTE", "中兴"},
	{"Lenovo", "联想"},
	{"Samsung", "三星"},
	{"motorola", "摩托罗拉"},
	{"Sony", "索尼"},
}

// translateBrand writes the maker at the start of name in the other
// language: "OnePlus Ace 3V" to "一加 Ace 3V", or back.
func translateBrand(name string, toChinese bool) string {
	for _, b := range brands {
		from, to := b.zh, b.en
		if toChinese {
			from, to = b.en, b.zh
		}
		if len(name) >= len(from) && strings.EqualFold(name[:len(from)], from) {
			return to + name[len(from):]
		}
	}
	return name
}

func isChinese(lang string) bool {
	return lang == "zh" || strings.HasPrefix(lang, "zh-") || strings.HasPrefix(lang, "zh_")
}

// NameIn is the device's name in a language, as the BCP 47 tag lang:
// "一加 Ace 3V" in Chinese ("zh-Hans"), "OnePlus Ace 3V" in others. A name
// of the language among the device's own comes first; else the maker in
// a name of the other language is translated, for the makers AirDroid
// knows; else the name as the device gives it.
func (i Info) NameIn(lang string) string {
	zh := isChinese(lang)
	for _, n := range i.MarketNames {
		if hasHan(n) == zh {
			return n
		}
	}
	if len(i.MarketNames) > 0 {
		return translateBrand(i.MarketNames[0], zh)
	}
	if zh {
		return translateBrand(i.Name(), true)
	}
	return i.Name()
}

// MakerIn is the maker's name in a language.
func (i Info) MakerIn(lang string) string {
	m := i.Maker()
	if isChinese(lang) {
		return translateBrand(m, true)
	}
	return m
}

// SubtitleIn is "OnePlus · PJF110 · Android 15" in a language.
func (i Info) SubtitleIn(lang string) string {
	var parts []string
	if m := i.MakerIn(lang); m != "" {
		parts = append(parts, m)
	}
	if i.Model != "" {
		parts = append(parts, i.Model)
	}
	if i.Release != "" {
		parts = append(parts, "Android "+i.Release)
	}
	return strings.Join(parts, " · ")
}
