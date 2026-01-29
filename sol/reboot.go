package sol

import (
	"regexp"
	"strings"
)

type RebootDetector struct {
	patterns []*regexp.Regexp
}

func NewRebootDetector(patterns []string) *RebootDetector {
	rd := &RebootDetector{
		patterns: make([]*regexp.Regexp, 0, len(patterns)),
	}

	for _, p := range patterns {
		// Case insensitive matching
		re, err := regexp.Compile("(?i)" + regexp.QuoteMeta(p))
		if err == nil {
			rd.patterns = append(rd.patterns, re)
		}
	}

	return rd
}

func (rd *RebootDetector) Check(text string) bool {
	// Common reboot indicators in Supermicro BIOS
	commonPatterns := []string{
		"Press <DEL>",
		"Press DEL",
		"Initializing",
		"BIOS Date",
		"Memory Test",
		"CPU Type",
	}

	text = strings.ToLower(text)

	for _, p := range rd.patterns {
		if p.MatchString(text) {
			return true
		}
	}

	for _, p := range commonPatterns {
		if strings.Contains(text, strings.ToLower(p)) {
			return true
		}
	}

	return false
}
