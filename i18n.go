package main

import (
	"fmt"
	"strings"
)

// Lang is a UI/runtime language code.
// Chinese remains the project default to keep Chinese-speaking operators first-class.
type Lang string

const (
	LangZH Lang = "zh"
	LangEN Lang = "en"
)

// normalizeLang maps free-form language tags to supported languages.
// Unknown / empty values default to Chinese.
func normalizeLang(value string) Lang {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return LangZH
	}
	if strings.HasPrefix(value, "en") {
		return LangEN
	}
	if strings.HasPrefix(value, "zh") {
		return LangZH
	}
	return LangZH
}

// messages holds operator-facing runtime copy.
// Machine classification keys stay stable; only human-readable text is localized.
var messages = map[string]map[Lang]string{
	"auth_expired": {
		LangZH: "认证已过期或失效",
		LangEN: "Authentication expired or invalid",
	},
	"quota_exhausted": {
		LangZH: "额度已用尽",
		LangEN: "Quota exhausted",
	},
	"temp_rate_limited": {
		LangZH: "临时限流 (HTTP 429)，建议稍后重试",
		LangEN: "Temporarily rate-limited (HTTP 429); retry later",
	},
	"permission_denied": {
		LangZH: "对话权限被拒绝",
		LangEN: "Chat permission denied",
	},
	"model_unavailable": {
		LangZH: "测试模型不可用",
		LangEN: "Probe model unavailable",
	},
	"chat_ok": {
		LangZH: "对话测试成功",
		LangEN: "Chat probe succeeded",
	},
	"probe_failed": {
		LangZH: "探测失败",
		LangEN: "Probe failed",
	},
	"unable_classify": {
		LangZH: "无法可靠分类",
		LangEN: "Unable to classify reliably",
	},
	"category_with_incremental": {
		LangZH: "分类巡检不能与增量巡检同时使用",
		LangEN: "Category inspection cannot be combined with incremental inspection",
	},
	"incremental_needs_results": {
		LangZH: "增量巡检需要已有结果，请先完整巡检",
		LangEN: "Incremental inspection requires existing results; run a full inspection first",
	},
	"category_needs_results": {
		LangZH: "分类巡检需要已有结果，请先完整巡检",
		LangEN: "Category inspection requires existing results; run a full inspection first",
	},
	"no_accounts_in_category": {
		LangZH: "当前分类下没有可巡检账号",
		LangEN: "No inspectable accounts in the current category",
	},
	"list_accounts_failed": {
		LangZH: "列出账号失败: %s",
		LangEN: "Failed to list accounts: %s",
	},
	"stopped_before_probe": {
		LangZH: "已停止，未探测",
		LangEN: "Stopped before probing",
	},
	"stopped": {
		LangZH: "已停止",
		LangEN: "Stopped",
	},
	"account_missing": {
		LangZH: "Auth 列表中已不存在该账号",
		LangEN: "Account no longer exists in the Auth list",
	},
	"probe_timeout": {
		LangZH: "探测超时（>%s）",
		LangEN: "Probe timed out (>%s)",
	},
	"missing_auth_index": {
		LangZH: "缺少 auth_index",
		LangEN: "Missing auth_index",
	},
	"fallback_disagreed": {
		LangZH: "；备用接口结果不一致，按主探测结果判定",
		LangEN: "; fallback endpoint disagreed; using primary probe result",
	},
	"http_probe_timeout": {
		LangZH: "HTTP 探测超时（%s）: %s %s",
		LangEN: "HTTP probe timed out (%s): %s %s",
	},
	"list_accounts_timeout": {
		LangZH: "列出账号超时（30s）",
		LangEN: "Listing accounts timed out (30s)",
	},
	"menu_name": {
		LangZH: "Grok 账号巡检",
		LangEN: "Grok Account Inspection",
	},
	"menu_desc": {
		LangZH: "Grok 账号巡检与自动禁用（free-usage / 403 / 401）。",
		LangEN: "Grok account inspection and auto-ban (free-usage / 403 / 401).",
	},
	"workers_range": {
		LangZH: "并发必须是 %d 到 %d 之间的整数",
		LangEN: "workers must be an integer between %d and %d",
	},
	"already_running": {
		LangZH: "巡检已在运行",
		LangEN: "inspection already running",
	},
	"busy_row_action": {
		LangZH: "忙碌：行操作进行中",
		LangEN: "busy: row action in progress",
	},

	"cfg_autoban_enabled": {
		LangZH: "是否启用自动禁用（free-usage / permission-denied / 401）。",
		LangEN: "Enable automatic ban for free-usage / permission-denied / 401.",
	},
	"cfg_fallback_hours": {
		LangZH: "没有准确恢复时间时，free-usage-exhausted 的禁用小时数，默认 24。",
		LangEN: "Ban hours for free-usage-exhausted when no exact restore time is known (default 24).",
	},
	"cfg_persist_state": {
		LangZH: "是否将自动禁用状态保存到 state_file。",
		LangEN: "Persist auto-ban state to state_file.",
	},
	"cfg_state_file": {
		LangZH: "自动禁用状态 JSON 路径；留空时使用 data/grok-inspection/bans.json。",
		LangEN: "Auto-ban state JSON path; empty uses data/grok-inspection/bans.json.",
	},
	"cfg_log_matches": {
		LangZH: "是否记录自动禁用命中日志。",
		LangEN: "Log auto-ban match events.",
	},
	"save_autoban_state_failed": {
		LangZH: "保存自动禁用状态失败: %s",
		LangEN: "Failed to save auto-ban state: %s",
	},

}

// T returns a localized message. Missing keys fall back to Chinese, then the key.
func T(lang Lang, key string, args ...any) string {
	lang = normalizeLang(string(lang))
	entry := messages[key]
	text := entry[lang]
	if text == "" {
		text = entry[LangZH]
	}
	if text == "" {
		text = key
	}
	if len(args) == 0 {
		return text
	}
	return fmt.Sprintf(text, args...)
}

// localizeKnownReason rewrites a previously stored reason into the requested language
// when it matches a known catalog value. Unknown free-form text is left unchanged.
func localizeKnownReason(lang Lang, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return reason
	}
	lang = normalizeLang(string(lang))
	for key, entry := range messages {
		// Only map human reason-like keys, not menu/control messages.
		switch key {
		case "menu_name", "menu_desc", "workers_range", "already_running", "busy_row_action",
			"category_with_incremental", "incremental_needs_results", "category_needs_results",
			"no_accounts_in_category", "list_accounts_failed", "list_accounts_timeout",
			"http_probe_timeout", "probe_timeout":
			continue
		}
		for _, candidate := range entry {
			if candidate == "" {
				continue
			}
			if reason == candidate {
				return T(lang, key)
			}
			// Handle "Permission denied (HTTP 403)" style suffixes.
			prefix := candidate + " (HTTP "
			if strings.HasPrefix(reason, prefix) && strings.HasSuffix(reason, ")") {
				return T(lang, key) + reason[len(candidate):]
			}
			// Handle fallback disagreement suffix appended to a known reason.
			for _, suffixKey := range []string{"fallback_disagreed"} {
				for _, suf := range messages[suffixKey] {
					if suf != "" && strings.HasSuffix(reason, suf) {
						base := strings.TrimSuffix(reason, suf)
						if base == candidate {
							return T(lang, key) + T(lang, suffixKey)
						}
						if strings.HasPrefix(base, candidate+" (HTTP ") {
							return T(lang, key) + base[len(candidate):] + T(lang, suffixKey)
						}
					}
				}
			}
		}
	}
	// Also map whole known control/error strings.
	for key, entry := range messages {
		for _, candidate := range entry {
			if candidate != "" && reason == candidate {
				return T(lang, key)
			}
		}
	}
	return reason
}
