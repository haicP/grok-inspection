package main

import "testing"

func TestNormalizeLangDefaultsToChinese(t *testing.T) {
	if normalizeLang("") != LangZH {
		t.Fatalf("empty lang should default to zh")
	}
	if normalizeLang("fr") != LangZH {
		t.Fatalf("unknown lang should default to zh")
	}
	if normalizeLang("en-US") != LangEN {
		t.Fatalf("en-US should map to en")
	}
	if normalizeLang("zh-CN") != LangZH {
		t.Fatalf("zh-CN should map to zh")
	}
}

func TestTReturnsChineseAndEnglish(t *testing.T) {
	zh := T(LangZH, "auth_expired")
	en := T(LangEN, "auth_expired")
	if zh == en {
		t.Fatalf("zh and en should differ: %q", zh)
	}
	if zh != "认证已过期或失效" {
		t.Fatalf("zh = %q", zh)
	}
	if en != "Authentication expired or invalid" {
		t.Fatalf("en = %q", en)
	}
}

func TestLocalizeKnownReasonRoundTrip(t *testing.T) {
	en := T(LangEN, "quota_exhausted")
	zh := localizeKnownReason(LangZH, en)
	if zh != T(LangZH, "quota_exhausted") {
		t.Fatalf("localize en->zh = %q", zh)
	}
	back := localizeKnownReason(LangEN, zh)
	if back != en {
		t.Fatalf("localize zh->en = %q", back)
	}
}
