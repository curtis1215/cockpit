package reporter

import "regexp"

var semver = regexp.MustCompile(`(\d+(?:\.\d+){1,3})`)

// ParseVersion mirrors the Python server's version_parse.parse_version:
// uses the custom regex when given (whole match if it has no capture group),
// else the default semver pattern. Returns "" when nothing matches or regex invalid.
func ParseVersion(text, customRegex string) string {
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
