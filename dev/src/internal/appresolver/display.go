package appresolver

import (
	"strings"
	"unicode"
)

var knownDisplayNames = map[string]string{
	"ai.perplexity.app.android":               "Perplexity",
	"ai.perplexity.comet":                     "Comet",
	"ai.x.grok":                               "Grok",
	"app.nicegram":                            "Nicegram",
	"app.revanced.android.youtube":            "YouTube ReVanced",
	"app.revanced.manager.flutter":            "ReVanced Manager",
	"app.rvx.android.youtube":                 "YouTube RVX",
	"bin.mt.plus":                             "MT Manager",
	"com.android.chrome":                      "Chrome",
	"com.android.providers.downloads":         "Downloads",
	"com.android.providers.downloads.ui":      "Downloads",
	"com.android.shell":                       "Android Shell",
	"com.android.vending":                     "Play Store",
	"com.apkpure.aegon":                       "APKPure",
	"com.binance.dev":                         "Binance",
	"com.bybit.app":                           "Bybit",
	"com.canva.editor":                        "Canva",
	"com.chrome.beta":                         "Chrome Beta",
	"com.cloudflare.onedotonedotonedotone":    "1.1.1.1",
	"com.discord":                             "Discord",
	"com.duolingo":                            "Duolingo",
	"com.facebook.katana":                     "Facebook",
	"com.github.android":                      "GitHub",
	"com.google.android.apps.authenticator2":  "Authenticator",
	"com.google.android.apps.googlevoice":     "Google Voice",
	"com.google.android.apps.photos":          "Google Photos",
	"com.google.android.apps.translate":       "Google Translate",
	"com.google.android.apps.walletnfcrel":    "Google Wallet",
	"com.google.android.apps.youtube.kids":    "YouTube Kids",
	"com.google.android.gm":                   "Gmail",
	"com.google.android.gms":                  "Google Play services",
	"com.google.android.googlequicksearchbox": "Google",
	"com.google.android.gsf":                  "Google Services Framework",
	"com.google.android.inputmethod.latin":    "Gboard",
	"com.google.android.youtube":              "YouTube",
	"com.instagram.android":                   "Instagram",
	"com.microsoft.copilot":                   "Copilot",
	"com.microsoft.emmx":                      "Edge",
	"com.microsoft.emmx.beta":                 "Edge Beta",
	"com.microsoft.emmx.canary":               "Edge Canary",
	"com.microsoft.emmx.dev":                  "Edge Dev",
	"com.microsoft.office.officehubrow":       "Microsoft 365",
	"com.microsoft.office.outlook":            "Outlook",
	"com.microsoft.translator":                "Microsoft Translator",
	"com.mojang.minecraftpe":                  "Minecraft",
	"com.okinc.okex.gp":                       "OKX",
	"com.omarea.vtools":                       "Scene",
	"com.openai.chatgpt":                      "ChatGPT",
	"com.paypal.android.p2pmobile":            "PayPal",
	"com.reddit.frontpage":                    "Reddit",
	"com.roblox.client":                       "Roblox",
	"com.spotify.music":                       "Spotify",
	"com.sukisu.ultra":                        "SukiSU Ultra",
	"com.tencent.mm":                          "微信",
	"com.termux":                              "Termux",
	"com.topjohnwu.magisk":                    "Magisk",
	"com.transferwise.android":                "Wise",
	"com.twitter.android":                     "X",
	"com.whatsapp":                            "WhatsApp",
	"com.xingin.xhs":                          "小红书",
	"com.zhiliaoapp.musically":                "TikTok",
	"io.github.huskydg.magisk":                "Kitsune Mask",
	"io.legado.app.release":                   "阅读",
	"li.songe.gkd":                            "GKD",
	"me.bmax.apatch":                          "APatch",
	"me.weishu.kernelsu":                      "KernelSU",
	"notion.id":                               "Notion",
	"org.mozilla.firefox":                     "Firefox",
	"org.telegram.messenger":                  "Telegram",
	"org.telegram.messenger.web":              "Telegram Web",
	"org.thoughtcrime.securesms":              "Signal",
	"org.thunderdog.challegram":               "Telegram X",
}

func DisplayName(packageName string, label string) string {
	if label = strings.TrimSpace(label); label != "" {
		return label
	}
	if name, ok := knownDisplayNames[packageName]; ok {
		return name
	}
	return humanizePackageName(packageName)
}

func AvatarText(displayName string, packageName string) string {
	text := strings.TrimSpace(displayName)
	if text == "" {
		text = packageName
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return strings.ToUpper(string(r))
		}
	}
	return "#"
}

func humanizePackageName(packageName string) string {
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return "Unknown"
	}
	if strings.HasPrefix(packageName, "org.chromium.webapk.") {
		return "WebAPK"
	}
	parts := strings.FieldsFunc(packageName, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" || isPackageNoise(part) {
			continue
		}
		return titleWords(splitCamel(part))
	}
	return packageName
}

func isPackageNoise(value string) bool {
	switch strings.ToLower(value) {
	case "android", "app", "apps", "mobile", "release", "debug", "free", "gp", "gplay", "google", "flutter":
		return true
	default:
		return false
	}
}

func splitCamel(value string) []string {
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	var words []string
	var current []rune
	var previous rune
	for _, r := range value {
		if unicode.IsUpper(r) && previous != 0 && !unicode.IsUpper(previous) && len(current) > 0 {
			words = append(words, string(current))
			current = current[:0]
		}
		current = append(current, r)
		previous = r
	}
	if len(current) > 0 {
		words = append(words, string(current))
	}
	return words
}

func titleWords(words []string) string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		for _, part := range strings.Fields(word) {
			lower := strings.ToLower(part)
			switch lower {
			case "youtube":
				out = append(out, "YouTube")
			case "github":
				out = append(out, "GitHub")
			default:
				runes := []rune(lower)
				if len(runes) > 0 {
					runes[0] = unicode.ToUpper(runes[0])
				}
				out = append(out, string(runes))
			}
		}
	}
	if len(out) == 0 {
		return "Unknown"
	}
	return strings.Join(out, " ")
}
