package pipeline

import (
	"strings"
	"testing"
)

func TestEvaluatePassesCleanText(t *testing.T) {
	f := NewPreFilter()

	cases := []string{
		"Ищем Go-разработчика на удалёнку, бюджет 300к",
		"Нужен парсер на Python для e-commerce сайта",
		"",
	}
	for _, text := range cases {
		pass, reason := f.Evaluate(text)
		if !pass {
			t.Errorf("Evaluate(%q) = false, %q; want pass", text, reason)
		}
		if reason != "" {
			t.Errorf("Evaluate(%q) reason = %q, want empty", text, reason)
		}
	}
}

func TestEvaluateRejectsSpamKeywords(t *testing.T) {
	f := NewPreFilter()

	cases := []struct {
		name string
		text string
		want string
	}{
		{"paid review", "Сделаю сайт ЗА ОТЗЫВ недорого", "за отзыв"},
		{"cheap", "Оплата копейки, но интересный проект", "копейки"},
		{"no experience ok", "Работа для новичков БЕЗ ОПЫТА", "без опыта"},
		{"roulette", "Участие в рулетка турнире", "рулетка"},
		{"scam", "Это скам, не связывайтесь", "скам"},
		{"casino", "Казино онлайн, нужны разработчики", "казино"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pass, reason := f.Evaluate(tc.text)
			if pass {
				t.Fatalf("Evaluate(%q) = true; want reject", tc.text)
			}
			if !strings.Contains(reason, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.want)
			}
		})
	}
}

func TestEvaluateCustomKeywords(t *testing.T) {
	f := NewPreFilter("gambling", "пирамида")

	if pass, _ := f.Evaluate("нормальный текст про Go"); !pass {
		t.Error("clean text rejected by custom filter")
	}
	if pass, reason := f.Evaluate("это GAMBLING проект"); pass {
		t.Error("gambling text passed custom filter")
	} else if !strings.Contains(reason, "gambling") {
		t.Errorf("reason = %q, want keyword mention", reason)
	}
	if pass, _ := f.Evaluate("финансовая пирамида"); pass {
		t.Error("piramida text passed custom filter")
	}
}

func TestEvaluateEmptyFilterPassesAll(t *testing.T) {
	var f *PreFilter // nil receiver must not panic
	if pass, _ := f.Evaluate("за отзыв казино скам"); !pass {
		t.Error("nil filter rejected text")
	}

	empty := NewPreFilter("", "   ")
	if pass, _ := empty.Evaluate("за отзыв"); !pass {
		t.Error("empty keyword filter rejected text")
	}
}
