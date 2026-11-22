package main

// Names for what the traffic analyzer sees: services by domain (DNS answers
// and TLS server names), by address range when no name is known, and
// protocols by port. The tables cover what is common on a travel router;
// a name that no table knows is shown as its registrable domain.

import (
	"net/netip"
	"strconv"
	"strings"
)

// Categories, as the analyzer shows them (English; the app's catalog
// translates them).
const (
	catVideo     = "Video"
	catMusic     = "Music"
	catSocial    = "Social"
	catMessaging = "Messaging"
	catCalls     = "Calls & meetings"
	catCloud     = "Cloud storage"
	catWeb       = "Web & search"
	catShopping  = "Shopping"
	catGaming    = "Gaming"
	catAI        = "AI"
	catWork      = "Work"
	catSystem    = "System & updates"
	catAds       = "Ads & tracking"
	catCDN       = "CDN & hosting"
	catVPN       = "VPN & tunnels"
	catDNS       = "DNS"
	catOther     = "Other"
)

const svcUnknown = "Unidentified"

type svcInfo struct{ name, cat string }

// serviceDomains maps a domain (and everything under it) to a service; the
// longest matching suffix wins.
var serviceDomains = map[string]svcInfo{}

func addService(name, cat string, domains ...string) {
	for _, d := range domains {
		serviceDomains[d] = svcInfo{name, cat}
	}
}

func init() {
	addService("YouTube", catVideo, "youtube.com", "googlevideo.com", "ytimg.com", "youtu.be", "youtube-nocookie.com",
		"yt3.ggpht.com", "youtubei.googleapis.com", "youtube.googleapis.com", "youtubekids.com")
	addService("YouTube Music", catMusic, "music.youtube.com")
	addService("Netflix", catVideo, "netflix.com", "nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net", "netflix.net")
	addService("Twitch", catVideo, "twitch.tv", "ttvnw.net", "jtvnw.net", "twitchcdn.net", "twitchsvc.net")
	addService("Prime Video", catVideo, "primevideo.com", "aiv-cdn.net", "aiv-delivery.net", "amazonvideo.com", "pv-cdn.net")
	addService("Disney+", catVideo, "disneyplus.com", "disney-plus.net", "dssott.com", "bamgrid.com")
	addService("Max", catVideo, "max.com", "hbomax.com", "hbo.com", "hbomaxcdn.com")
	addService("Apple TV", catVideo, "tv.apple.com", "hls.itunes.apple.com")
	addService("Vimeo", catVideo, "vimeo.com", "vimeocdn.com")
	addService("Dailymotion", catVideo, "dailymotion.com", "dm-event.net", "dmcdn.net")
	addService("Rutube", catVideo, "rutube.ru", "rutubelist.ru")
	addService("Kinopoisk", catVideo, "kinopoisk.ru", "kp.yandex.net", "strm.yandex.net")
	addService("ivi", catVideo, "ivi.ru", "ivi.tv")
	addService("Okko", catVideo, "okko.tv")
	addService("Plex", catVideo, "plex.tv", "plex.direct")

	addService("Spotify", catMusic, "spotify.com", "scdn.co", "spotifycdn.com", "spotifycdn.net", "pscdn.co", "spotify.design")
	addService("Apple Music", catMusic, "music.apple.com", "aod.itunes.apple.com", "audio-ssl.itunes.apple.com")
	addService("Deezer", catMusic, "deezer.com", "dzcdn.net")
	addService("SoundCloud", catMusic, "soundcloud.com", "sndcdn.com")
	addService("Yandex Music", catMusic, "music.yandex.ru", "music.yandex.net", "music.yandex.com")
	addService("Tidal", catMusic, "tidal.com", "tidalhifi.com")

	addService("Facebook", catSocial, "facebook.com", "fbcdn.net", "facebook.net", "fb.com", "fbsbx.com", "fb.me")
	addService("Instagram", catSocial, "instagram.com", "cdninstagram.com", "ig.me")
	addService("Threads", catSocial, "threads.net", "threads.com")
	addService("X", catSocial, "twitter.com", "x.com", "twimg.com", "t.co", "twttr.com")
	addService("TikTok", catSocial, "tiktok.com", "tiktokv.com", "tiktokv.us", "tiktokcdn.com", "tiktokcdn-us.com",
		"tiktokcdn-eu.com", "byteoversea.com", "ibytedtos.com", "ibyteimg.com", "muscdn.com", "tiktokw.us", "tiktokw.eu")
	addService("Reddit", catSocial, "reddit.com", "redd.it", "redditmedia.com", "redditstatic.com")
	addService("LinkedIn", catSocial, "linkedin.com", "licdn.com")
	addService("Pinterest", catSocial, "pinterest.com", "pinimg.com")
	addService("Snapchat", catSocial, "snapchat.com", "sc-cdn.net", "snap-dev.net", "snapkit.com", "sc-static.net")
	addService("VK", catSocial, "vk.com", "vk.me", "vk.ru", "userapi.com", "vkuser.net", "vkuseraudio.net",
		"vkuservideo.net", "vk-cdn.net", "vkontakte.ru", "mvk.com", "vk-portal.net")
	addService("OK", catSocial, "ok.ru", "mycdn.me", "odnoklassniki.ru")
	addService("Dzen", catSocial, "dzen.ru")
	addService("Tumblr", catSocial, "tumblr.com")

	addService("WhatsApp", catMessaging, "whatsapp.com", "whatsapp.net", "wa.me", "whatsapp.biz")
	addService("Telegram", catMessaging, "telegram.org", "t.me", "telegram.me", "tdesktop.com", "telesco.pe",
		"telegra.ph", "cdn-telegram.org", "telegram-cdn.org")
	addService("Signal", catMessaging, "signal.org", "whispersystems.org", "signal.group", "signal.me")
	addService("Viber", catMessaging, "viber.com")
	addService("Messenger", catMessaging, "messenger.com")
	addService("Discord", catMessaging, "discord.com", "discord.gg", "discordapp.com", "discordapp.net",
		"discord.media", "discordcdn.com")
	addService("Max (VK)", catMessaging, "max.ru", "oneme.ru")

	addService("Zoom", catCalls, "zoom.us", "zoom.com", "zoomgov.com", "zoomcdn.com")
	addService("Microsoft Teams", catCalls, "teams.microsoft.com", "teams.live.com", "teams.cdn.office.net", "skype.com", "lync.com")
	addService("Google Meet", catCalls, "meet.google.com", "meetings.googleapis.com")
	addService("Webex", catCalls, "webex.com")

	addService("iCloud", catCloud, "icloud.com", "icloud-content.com", "apple-cloudkit.com", "cloudkit.apple.com")
	addService("Google Drive", catCloud, "drive.google.com", "docs.google.com", "drive.usercontent.google.com")
	addService("Dropbox", catCloud, "dropbox.com", "dropboxapi.com", "dropboxusercontent.com", "dropboxstatic.com")
	addService("OneDrive", catCloud, "onedrive.live.com", "1drv.com", "onedrive.com", "1drv.ms")
	addService("Yandex Disk", catCloud, "disk.yandex.ru", "disk.yandex.net", "disk.yandex.com")
	addService("MEGA", catCloud, "mega.nz", "mega.co.nz", "mega.io")
	addService("Box", catCloud, "box.com", "boxcdn.net")

	addService("Google", catWeb, "google.com", "gstatic.com", "googleapis.com", "googleusercontent.com", "gvt1.com",
		"gvt2.com", "gvt3.com", "ggpht.com", "google.co", "withgoogle.com", "googlezip.net")
	addService("Gemini", catAI, "gemini.google.com", "bard.google.com", "generativelanguage.googleapis.com")
	addService("Bing", catWeb, "bing.com", "bing.net")
	addService("DuckDuckGo", catWeb, "duckduckgo.com")
	addService("Yandex", catWeb, "yandex.ru", "yandex.net", "yandex.com", "ya.ru", "yastatic.net", "yandex.rs", "yandex.by", "yandex.kz")
	addService("Wikipedia", catWeb, "wikipedia.org", "wikimedia.org", "wikidata.org")
	addService("Mail.ru", catWeb, "mail.ru", "imgsmail.ru", "mradx.net", "mrgcdn.ru")
	addService("Habr", catWeb, "habr.com", "habrastorage.org")
	addService("Medium", catWeb, "medium.com")

	addService("Amazon", catShopping, "amazon.com", "media-amazon.com", "ssl-images-amazon.com", "amazon.de", "amazon.co.uk")
	addService("AliExpress", catShopping, "aliexpress.com", "aliexpress.ru", "alicdn.com", "alibaba.com", "aliexpress.us")
	addService("Ozon", catShopping, "ozon.ru", "ozone.ru", "ozonusercontent.com", "ozon.travel")
	addService("Wildberries", catShopping, "wildberries.ru", "wb.ru", "wbbasket.ru", "wbstatic.net", "wbx-content-v2.wbstatic.net")
	addService("eBay", catShopping, "ebay.com", "ebayimg.com", "ebaystatic.com")
	addService("Avito", catShopping, "avito.ru", "avito.st")
	addService("Temu", catShopping, "temu.com", "kwcdn.com")
	addService("SHEIN", catShopping, "shein.com", "ltwebstatic.com")

	addService("Steam", catGaming, "steampowered.com", "steamcommunity.com", "steamcontent.com", "steamstatic.com",
		"steamserver.net", "valvesoftware.com", "steamusercontent.com", "steamgames.com")
	addService("Epic Games", catGaming, "epicgames.com", "epicgames.dev", "unrealengine.com", "fortnite.com")
	addService("PlayStation", catGaming, "playstation.com", "playstation.net", "sonyentertainmentnetwork.com")
	addService("Xbox", catGaming, "xboxlive.com", "xbox.com")
	addService("Nintendo", catGaming, "nintendo.com", "nintendo.net")
	addService("Riot Games", catGaming, "riotgames.com", "leagueoflegends.com", "riotcdn.net")
	addService("Roblox", catGaming, "roblox.com", "rbxcdn.com")
	addService("Battle.net", catGaming, "blizzard.com", "battle.net", "blzstatic.com")
	addService("EA", catGaming, "ea.com", "origin.com")
	addService("Minecraft", catGaming, "minecraft.net", "mojang.com")

	addService("ChatGPT", catAI, "openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com")
	addService("Claude", catAI, "anthropic.com", "claude.ai", "claude.com", "claudeusercontent.com")
	addService("Perplexity", catAI, "perplexity.ai")
	addService("DeepSeek", catAI, "deepseek.com")
	addService("Hugging Face", catAI, "huggingface.co", "hf.co")
	addService("Mistral", catAI, "mistral.ai")

	addService("Microsoft 365", catWork, "office.com", "office365.com", "office.net", "officeapps.live.com", "outlook.com",
		"outlook.office365.com", "microsoftonline.com", "sharepoint.com", "office.microsoft.com", "m365.cloud.microsoft")
	addService("Slack", catWork, "slack.com", "slack-edge.com", "slack-msgs.com", "slack-imgs.com")
	addService("Notion", catWork, "notion.so", "notion.site", "notion.com")
	addService("GitHub", catWork, "github.com", "githubusercontent.com", "githubassets.com", "github.io", "ghcr.io")
	addService("GitLab", catWork, "gitlab.com")
	addService("Atlassian", catWork, "atlassian.net", "atlassian.com", "jira.com", "trello.com")
	addService("Figma", catWork, "figma.com")
	addService("Stack Overflow", catWork, "stackoverflow.com", "sstatic.net", "stackexchange.com")

	addService("Apple", catSystem, "apple.com", "mzstatic.com", "cdn-apple.com", "aaplimg.com", "apple-dns.net",
		"apple.news", "itunes.com", "appattest.apple.com", "apple-mapkit.com")
	addService("Apple Push", catSystem, "push.apple.com", "push-apple.com.akadns.net")
	addService("App Store", catSystem, "itunes.apple.com", "apps.apple.com", "appldnld.apple.com", "updates.cdn-apple.com")
	addService("iCloud Private Relay", catVPN, "mask.icloud.com", "mask-h2.icloud.com", "mask-api.icloud.com")
	addService("Google Play", catSystem, "play.google.com", "android.com", "play.googleapis.com", "android.clients.google.com")
	addService("Microsoft", catSystem, "microsoft.com", "windows.com", "windowsupdate.com", "msftconnecttest.com",
		"msedge.net", "live.com", "msn.com", "microsoft.net", "msauth.net", "msftauth.net", "windows.net")
	addService("Samsung", catSystem, "samsung.com", "samsungcloud.com", "samsungapps.com", "samsungdm.com")
	addService("Xiaomi", catSystem, "xiaomi.com", "miui.com", "xiaomi.net", "mi.com")
	addService("GL.iNet", catSystem, "gl-inet.com", "goodcloud.xyz", "glinet.com", "gl-inet.cn")
	addService("OpenWrt", catSystem, "openwrt.org")
	addService("Linux updates", catSystem, "ubuntu.com", "canonical.com", "debian.org", "snapcraft.io", "raspberrypi.com", "raspberrypi.org")
	addService("Firefox", catSystem, "mozilla.org", "mozilla.com", "mozilla.net", "firefox.com")

	addService("Google Ads", catAds, "doubleclick.net", "googlesyndication.com", "googleadservices.com", "google-analytics.com",
		"googletagmanager.com", "googletagservices.com", "adservice.google.com", "app-measurement.com", "pagead2.googlesyndication.com")
	addService("Firebase", catAds, "crashlytics.com", "firebaselogging.googleapis.com", "firebase-settings.crashlytics.com")
	addService("Yandex Metrica", catAds, "mc.yandex.ru", "mc.yandex.com", "an.yandex.ru", "yandexadexchange.net", "appmetrica.yandex.ru",
		"appmetrica.yandex.com", "adfox.ru")
	addService("Meta Ads", catAds, "an.facebook.com", "graph.facebook.com")
	addService("Ad networks", catAds, "adnxs.com", "criteo.com", "criteo.net", "scorecardresearch.com", "appsflyer.com",
		"adjust.com", "branch.io", "doubleverify.com", "moatads.com", "taboola.com", "outbrain.com", "applovin.com",
		"unityads.unity3d.com", "ads-twitter.com", "amazon-adsystem.com", "rubiconproject.com", "pubmatic.com",
		"openx.net", "smartadserver.com", "adcolony.com", "chartboost.com", "inmobi.com", "mopub.com", "appsflyersdk.com",
		"sentry.io", "segment.io", "mixpanel.com", "amplitude.com", "hotjar.com", "clarity.ms")

	addService("Akamai", catCDN, "akamaized.net", "akamaihd.net", "akamai.net", "akamaiedge.net", "edgekey.net",
		"edgesuite.net", "akamaitechnologies.com", "akadns.net")
	addService("CloudFront", catCDN, "cloudfront.net")
	addService("Fastly", catCDN, "fastly.net", "fastlylb.net", "fastly-edge.com", "fastly.com")
	addService("Cloudflare", catCDN, "cloudflare.net", "cloudflare.com", "cdnjs.cloudflare.com", "cloudflarestorage.com",
		"workers.dev", "pages.dev", "cloudflare-ech.com")
	addService("AWS", catCDN, "amazonaws.com", "awsstatic.com", "aws.amazon.com")
	addService("Azure", catCDN, "azureedge.net", "azurefd.net", "azure.com", "cloudapp.net", "trafficmanager.net", "blob.core.windows.net")
	addService("jsDelivr", catCDN, "jsdelivr.net")
	addService("Bunny CDN", catCDN, "b-cdn.net", "bunnycdn.com")
	addService("Edgio", catCDN, "edgecastcdn.net", "llnwd.net", "llnwi.net")

	addService("Tailscale", catVPN, "tailscale.com", "tailscale.io", "ts.net")
	addService("NordVPN", catVPN, "nordvpn.com", "nordcdn.com")
	addService("ProtonVPN", catVPN, "protonvpn.com", "proton.me", "protonmail.com")
	addService("Mullvad", catVPN, "mullvad.net")
	addService("Tor", catVPN, "torproject.org", "onion")

	addService("Encrypted DNS", catDNS, "cloudflare-dns.com", "one.one.one.one", "dns.google", "dns.quad9.net",
		"dns.adguard.com", "dns.adguard-dns.com", "dns.nextdns.io", "doh.opendns.com", "mozilla.cloudflare-dns.com",
		"dns.yandex.ru", "common.dot.dns.yandex.net")
}

// Registrable domains under these two-label public suffixes take three
// labels (bbc.co.uk, not co.uk).
var secondLevelSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true, "me.uk": true, "com.au": true, "net.au": true,
	"org.au": true, "co.jp": true, "ne.jp": true, "or.jp": true, "com.br": true, "com.tr": true, "co.kr": true,
	"co.in": true, "com.cn": true, "net.cn": true, "org.cn": true, "com.ua": true, "co.rs": true, "org.rs": true,
	"in.rs": true, "edu.rs": true, "com.mx": true, "com.ar": true, "co.za": true, "co.nz": true, "com.sg": true,
	"com.hk": true, "com.tw": true, "co.il": true, "com.my": true, "co.th": true, "com.vn": true, "com.ge": true,
	"msk.ru": true, "spb.ru": true, "com.ru": true, "co.id": true, "com.pl": true, "com.cy": true, "ac.cy": true,
}

// normalizeHost lower-cases a host name and drops a trailing dot; it
// returns "" for anything that is not a plausible name.
func normalizeHost(s string) string {
	s = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
	if len(s) == 0 || len(s) > 253 || !strings.Contains(s, ".") {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_') {
			return ""
		}
	}
	return s
}

// registrableDomain is the part of a name its owner registered:
// "r3---sn-abc.googlevideo.com" → "googlevideo.com".
func registrableDomain(name string) string {
	labels := strings.Split(name, ".")
	n := len(labels)
	if n <= 2 {
		return name
	}
	if secondLevelSuffixes[labels[n-2]+"."+labels[n-1]] {
		return strings.Join(labels[n-3:], ".")
	}
	return labels[n-2] + "." + labels[n-1]
}

// classifyName gives the service of a host name, or the registrable
// domain with the category Other when no table knows it.
func classifyName(name string) svcInfo {
	if name == "" {
		return svcInfo{svcUnknown, catOther}
	}
	for s := name; s != ""; {
		if v, ok := serviceDomains[s]; ok {
			return v
		}
		i := strings.IndexByte(s, '.')
		if i < 0 {
			break
		}
		s = s[i+1:]
	}
	// google.rs, amazon.co.uk and the like.
	reg := registrableDomain(name)
	switch strings.SplitN(reg, ".", 2)[0] {
	case "google":
		return svcInfo{"Google", catWeb}
	case "amazon":
		return svcInfo{"Amazon", catShopping}
	case "yandex":
		return svcInfo{"Yandex", catWeb}
	}
	return svcInfo{reg, catOther}
}

type ipRange struct {
	p   netip.Prefix
	svc svcInfo
}

// Ranges of services that are often reached without a name the analyzer
// could see (apps with built-in addresses, encrypted DNS on the device).
var serviceRanges = func() []ipRange {
	var out []ipRange
	add := func(name, cat string, prefixes ...string) {
		for _, s := range prefixes {
			out = append(out, ipRange{netip.MustParsePrefix(s), svcInfo{name, cat}})
		}
	}
	add("Telegram", catMessaging, "91.105.192.0/23", "91.108.4.0/22", "91.108.8.0/22", "91.108.12.0/22",
		"91.108.16.0/22", "91.108.20.0/22", "91.108.56.0/22", "95.161.64.0/20", "149.154.160.0/20", "185.76.151.0/24",
		"2001:67c:4e8::/48", "2001:b28:f23c::/48", "2001:b28:f23d::/48", "2001:b28:f23f::/48", "2a0a:f280::/32")
	add("Apple", catSystem, "17.0.0.0/8", "2620:149::/32", "2a01:b740::/32")
	add("Meta", catSocial, "157.240.0.0/16", "31.13.64.0/18", "69.171.224.0/19", "102.132.96.0/20", "163.70.128.0/17",
		"179.60.192.0/22", "185.60.216.0/22", "2a03:2880::/32")
	add("Google", catWeb, "142.250.0.0/15", "172.217.0.0/16", "216.58.192.0/19", "172.253.0.0/16", "74.125.0.0/16",
		"64.233.160.0/19", "108.177.0.0/17", "173.194.0.0/16", "209.85.128.0/17", "216.239.32.0/19", "2a00:1450::/32",
		"2404:6800::/32", "2607:f8b0::/32", "2800:3f0::/32", "2c0f:fb50::/32")
	add("Cloudflare", catCDN, "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "162.158.0.0/15", "188.114.96.0/20",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "198.41.128.0/17", "173.245.48.0/20",
		"103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "131.0.72.0/22", "1.1.1.0/24", "1.0.0.0/24",
		"2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32")
	add("Yandex", catWeb, "5.255.192.0/18", "77.88.0.0/18", "87.250.224.0/19", "93.158.128.0/18", "213.180.192.0/19",
		"2a02:6b8::/32")
	add("VK", catSocial, "87.240.128.0/18", "93.186.224.0/20", "95.142.192.0/20", "2a00:bdc0::/32")
	add("Microsoft", catSystem, "13.64.0.0/11", "20.33.0.0/16", "20.40.0.0/13", "20.128.0.0/16", "40.64.0.0/10",
		"52.96.0.0/12", "104.40.0.0/13", "2603:1000::/24")
	return out
}()

func classifyIP(a netip.Addr) (svcInfo, bool) {
	a = a.Unmap()
	for _, r := range serviceRanges {
		if r.p.Contains(a) {
			return r.svc, true
		}
	}
	return svcInfo{}, false
}

// IP protocol numbers.
const (
	ipICMP   = 1
	ipTCP    = 6
	ipUDP    = 17
	ipGRE    = 47
	ipESP    = 50
	ipICMPv6 = 58
)

var portNames = map[string]string{
	"tcp/443": "HTTPS", "udp/443": "QUIC", "tcp/80": "HTTP", "udp/53": "DNS", "tcp/53": "DNS",
	"tcp/853": "DNS over TLS", "udp/853": "DNS over QUIC", "udp/123": "NTP", "udp/5060": "SIP", "tcp/5060": "SIP",
	"tcp/5061": "SIP TLS", "udp/500": "IPsec", "udp/4500": "IPsec", "udp/3478": "STUN", "udp/19302": "STUN",
	"udp/51820": "WireGuard", "udp/1194": "OpenVPN", "tcp/1194": "OpenVPN", "tcp/22": "SSH", "tcp/993": "IMAP",
	"tcp/143": "IMAP", "tcp/465": "SMTP", "tcp/587": "SMTP", "tcp/25": "SMTP", "tcp/995": "POP3", "tcp/5228": "Google push",
	"tcp/5223": "Apple push", "udp/41641": "Tailscale", "tcp/8443": "HTTPS (8443)", "tcp/9001": "Tor", "tcp/9030": "Tor",
	"tcp/5222": "XMPP", "tcp/1935": "RTMP", "tcp/1883": "MQTT", "tcp/8883": "MQTT", "udp/3479": "STUN",
	"udp/3480": "STUN", "udp/3481": "STUN", "tcp/3389": "Remote desktop", "udp/3389": "Remote desktop",
	"tcp/8080": "HTTP (8080)", "udp/1701": "L2TP", "tcp/1723": "PPTP", "udp/5353": "mDNS", "udp/67": "DHCP",
	"tcp/21": "FTP", "tcp/23": "Telnet", "tcp/6881": "BitTorrent", "udp/6881": "BitTorrent",
}

// protoName labels a flow by its protocol and the remote port.
func protoName(proto uint8, rport uint16) string {
	switch proto {
	case ipTCP, ipUDP:
		p := "tcp/"
		if proto == ipUDP {
			p = "udp/"
		}
		if n, ok := portNames[p+strconv.Itoa(int(rport))]; ok {
			return n
		}
		if proto == ipTCP {
			return "Other TCP"
		}
		return "Other UDP"
	case ipICMP, ipICMPv6:
		return "ICMP"
	case ipESP:
		return "IPsec"
	case ipGRE:
		return "GRE"
	}
	return "IP " + strconv.Itoa(int(proto))
}

// portService names services that a port says more about than any name.
func portService(proto uint8, rport uint16) (svcInfo, bool) {
	switch {
	case rport == 53 && (proto == ipTCP || proto == ipUDP):
		return svcInfo{"DNS", catDNS}, true
	case rport == 853:
		return svcInfo{"Encrypted DNS", catDNS}, true
	case rport == 123 && proto == ipUDP:
		return svcInfo{"Time sync", catSystem}, true
	case proto == ipESP, rport == 500 && proto == ipUDP, rport == 4500 && proto == ipUDP:
		return svcInfo{"IPsec", catVPN}, true
	case rport == 51820 && proto == ipUDP:
		return svcInfo{"WireGuard", catVPN}, true
	case rport == 1194:
		return svcInfo{"OpenVPN", catVPN}, true
	case rport == 41641 && proto == ipUDP:
		return svcInfo{"Tailscale", catVPN}, true
	case rport == 5223 && proto == ipTCP:
		return svcInfo{"Apple Push", catSystem}, true
	case rport == 5228 && proto == ipTCP:
		return svcInfo{"Google push", catSystem}, true
	case (rport == 9001 || rport == 9030) && proto == ipTCP:
		return svcInfo{"Tor", catVPN}, true
	case (rport == 5060 || rport == 5061) && (proto == ipTCP || proto == ipUDP):
		return svcInfo{"SIP calls", catCalls}, true
	}
	return svcInfo{}, false
}

// flowService decides the service of a flow: a telling port first, then
// the host name, then the address range.
func flowService(name string, remote netip.Addr, proto uint8, rport uint16) svcInfo {
	if s, ok := portService(proto, rport); ok {
		return s
	}
	if name != "" {
		return classifyName(name)
	}
	if s, ok := classifyIP(remote); ok {
		return s
	}
	return svcInfo{svcUnknown, catOther}
}
