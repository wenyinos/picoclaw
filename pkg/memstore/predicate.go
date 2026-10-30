package memstore

import "strings"

// Canonical predicate vocabulary. Unknown values are normalized to
// "待分类" instead of being allowed to become a new catch-all bucket.
var predicateAliases = map[string]string{
	"身份伪装":    "身份关系",
	"推演问答":    "剧情推演",
	"第一次":     "事件·第一次",
	"第二次":     "事件·第二次",
	"外貌造型":    "外貌",
	"职业背景":    "职业",
	"人物心理":    "心理状态",
	"结局走向":    "结局设定",
	"写作边界":    "写作规则",
	"profile": "用户画像",
}

var allowedPredicates = map[string]bool{
	"身份关系": true, "剧情推演": true, "事件·第一次": true, "事件·第二次": true,
	"外貌": true, "权力结构": true, "职业": true, "时间线": true,
	"心理状态": true, "结局设定": true, "写作规则": true, "人物档案": true,
	"历史版本": true, "旧摘要": true, "待归档": true, "工程": true,
	"用户画像": true, "待分类": true, "偏好": true,
	"nickname": true, "user_name": true, "ai_name": true,
	"birthday": true, "anniversary": true, "timezone": true,
	"language": true, "occupation": true, "city": true,
	"称呼": true, "阶段": true, "日期": true,
}

var scalarPredicates = map[string]bool{
	"nickname": true, "user_name": true, "ai_name": true,
	"birthday": true, "anniversary": true, "timezone": true,
	"language": true, "occupation": true, "city": true,
	"称呼": true, "阶段": true, "日期": true,
}

// NormalizePredicate maps known aliases to the canonical vocabulary and
// classifies unknown values instead of preserving another catch-all label.
func NormalizePredicate(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "待分类"
	}
	if mapped, ok := predicateAliases[v]; ok {
		return mapped
	}
	if allowedPredicates[v] {
		return v
	}
	return "待分类"
}

func isScalarPredicate(v string) bool {
	return scalarPredicates[strings.TrimSpace(v)]
}
