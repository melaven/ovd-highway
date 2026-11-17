package lane

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	budgetRe  = regexp.MustCompile(`(?i)(\d[\d\s]*)\s*(руб|₽|rur|rub)`)
	stopWords = []string{"tilda", "тильда", "битрикс", "bitrix", "1с", "1c", "wordpress", "диплом", "реферат", "курсовая"}
)

func PassesPreFilter(text string) bool {
	if utf8.RuneCountInString(text) < 40 {
		return false
	}

	lowerText := strings.ToLower(text)
	for _, word := range stopWords {
		if strings.Contains(lowerText, word) {
			return false
		}
	}

	matches := budgetRe.FindAllStringSubmatch(lowerText, -1)
	for _, match := range matches {
		if len(match) > 1 {
			budgetStr := strings.ReplaceAll(match[1], " ", "")
			budget, _ := strconv.Atoi(budgetStr)
			if budget > 0 && budget < 5000 {
				return false
			}
		}
	}

	return true
}
