package version

import (
	"regexp"
	"strconv"
	"strings"
)

// 預設版號：1.2 / 1.2.3 / 1.2.3.4，可選 npm/calver 後綴 -2、-beta.1（勿吃掉括號後文字）。
var semver = regexp.MustCompile(`(\d+(?:\.\d+){1,3}(?:-[0-9A-Za-z.]+)?)`)

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

// key 把 "1.2.3" 拆成整數切片；任一段非純數字則失敗。
func key(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
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

// parseVersion 支援 npm/calver 後綴：2026.7.1-2、v1.2.3+build。
// core 為主版本；pre 為 - 後的數值段（純數字 / 點分數字）；無法解析 core 則 ok=false。
func parseVersion(v string) (core, pre []int, ok bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" {
		return nil, nil, false
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	coreStr, preStr := v, ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		coreStr, preStr = v[:i], v[i+1:]
	}
	core, ok = key(coreStr)
	if !ok {
		return nil, nil, false
	}
	if preStr == "" {
		return core, nil, true
	}
	// "2" / "2.1" 直接解析；"rc.1" 等非純數字段則只抽數字 run，抽不到則忽略後綴。
	if pre, ok = key(preStr); ok {
		return core, pre, true
	}
	return core, digitRuns(preStr), true
}

func digitRuns(s string) []int {
	var out []int
	i := 0
	for i < len(s) {
		if s[i] < '0' || s[i] > '9' {
			i++
			continue
		}
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		n, err := strconv.Atoi(s[i:j])
		if err == nil {
			out = append(out, n)
		}
		i = j
	}
	return out
}

func cmpInts(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// behindCount 在 core 整段落後時估距離；僅 pre 不同則回 1。
func behindCount(ck, lk []int) int {
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
	eq := true
	for i := 0; i < n-1; i++ {
		if ck[i] != lk[i] {
			eq = false
			break
		}
	}
	if !eq {
		return 1
	}
	behind := lk[n-1] - ck[n-1]
	if behind < 1 {
		return 1
	}
	return behind
}

// Compare 回 (status, behindCount)。
// current/latest 任一空或 core 不可解析 → ("unknown",0)；
// current>=latest → ("up_to_date",0)；否則 ("behind", N)。
// 後綴規則（對齊 npm latest 語意）：核心相同時，有較大 -N 視為較新（2026.7.1 < 2026.7.1-2）。
func Compare(current, latest string) (string, int) {
	if current == "" || latest == "" {
		return "unknown", 0
	}
	ck, cpre, ok1 := parseVersion(current)
	lk, lpre, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return "unknown", 0
	}
	switch cmpInts(ck, lk) {
	case 1:
		return "up_to_date", 0
	case -1:
		return "behind", behindCount(ck, lk)
	default:
		// core 相同：比 pre（空 pre 視為全 0，故 2026.7.1 < 2026.7.1-2）
		switch cmpInts(cpre, lpre) {
		case 1, 0:
			return "up_to_date", 0
		default:
			return "behind", 1
		}
	}
}
