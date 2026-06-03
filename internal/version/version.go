package version

import (
	"regexp"
	"strconv"
	"strings"
)

var semver = regexp.MustCompile(`(\d+(?:\.\d+){1,3})`)

// Parse 從文字抽版本：customRegex 為空用預設 semver（group1）；自訂 regex 有 capture group 用 group1、否則整段；非法 regex 回 ""。
func Parse(text, customRegex string) string {
	re := semver
	group := 1
	if customRegex != "" {
		r, err := regexp.Compile(customRegex)
		if err != nil {
			return ""
		}
		re = r
		if re.NumSubexp() == 0 {
			group = 0
		}
	}
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[group]
}

func key(v string) ([]int, bool) {
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// Compare 回 (status, behindCount)。current/latest 任一空或不可解析 → ("unknown",0)；current>=latest → ("up_to_date",0)；否則 ("behind", N)。
func Compare(current, latest string) (string, int) {
	if current == "" || latest == "" {
		return "unknown", 0
	}
	ck, ok1 := key(current)
	lk, ok2 := key(latest)
	if !ok1 || !ok2 {
		return "unknown", 0
	}
	n := len(ck)
	if len(lk) > n {
		n = len(lk)
	}
	pad := func(a []int) []int {
		for len(a) < n {
			a = append(a, 0)
		}
		return a
	}
	ck, lk = pad(ck), pad(lk)
	cmp := 0
	for i := 0; i < n; i++ {
		if ck[i] != lk[i] {
			if ck[i] < lk[i] {
				cmp = -1
			} else {
				cmp = 1
			}
			break
		}
	}
	if cmp >= 0 {
		return "up_to_date", 0
	}
	behind := 1
	eq := true
	for i := 0; i < n-1; i++ {
		if ck[i] != lk[i] {
			eq = false
			break
		}
	}
	if eq {
		behind = lk[n-1] - ck[n-1]
		if behind < 1 {
			behind = 1
		}
	}
	return "behind", behind
}
