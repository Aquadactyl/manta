// Package termcolor converts colorstring-style markup into ANSI escape codes.
package termcolor

import (
	"regexp"
	"strings"
)

var tags = map[string]string{
	"default": "39", "black": "30", "red": "31", "green": "32", "yellow": "33", "blue": "34", "magenta": "35", "cyan": "36",
	"light_gray": "37", "dark_gray": "90", "light_red": "91", "light_green": "92", "light_yellow": "93", "light_blue": "94", "light_magenta": "95", "light_cyan": "96", "white": "97",
	"_default_": "49", "_black_": "40", "_red_": "41", "_green_": "42", "_yellow_": "43", "_blue_": "44", "_magenta_": "45", "_cyan_": "46",
	"_light_gray_": "47", "_dark_gray_": "100", "_light_red_": "101", "_light_green_": "102", "_light_yellow_": "103", "_light_blue_": "104", "_light_magenta_": "105", "_light_cyan_": "106", "_white_": "107",
	"bold": "1", "dim": "2", "underline": "4", "blink_slow": "5", "blink_fast": "6", "invert": "7", "hidden": "8", "reset": "0", "reset_bold": "21",
}

var markup = regexp.MustCompile(`(?i)\[[a-z0-9_-]+\]`)

// Color expands recognized colorstring-style tags and appends an ANSI reset
// when the input contains at least one recognized tag.
func Color(value string) string {
	colored := false
	result := markup.ReplaceAllStringFunc(value, func(match string) string {
		code, ok := tags[strings.ToLower(match[1:len(match)-1])]
		if !ok {
			return match
		}
		colored = true
		return "\033[" + code + "m"
	})
	if colored {
		result += "\033[0m"
	}
	return result
}
