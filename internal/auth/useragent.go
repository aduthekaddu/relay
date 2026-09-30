package auth

import "strings"

// UserAgent is a coarse description of a browser, good enough to label a
// device session ("Safari on iPhone"). Versions are deliberately omitted.
type UserAgent struct {
	Device  string // iPhone, iPad, Android phone, Android tablet, Mac, Windows PC, Linux PC, Chromebook, Command line
	Browser string // Safari, Chrome, Firefox, Edge, Opera, Samsung Internet, Brave, Vivaldi, curl, …
	OS      string // iOS, iPadOS, Android, macOS, Windows, Linux, ChromeOS
}

// ParseUserAgent extracts device, browser and OS from a User-Agent header.
// Unknown values are left empty. Order matters: many browsers claim to be
// several others (Edge says Chrome and Safari; Chrome says Safari).
func ParseUserAgent(ua string) UserAgent {
	var out UserAgent
	if len(ua) > 512 {
		ua = ua[:512]
	}
	has := func(s string) bool { return strings.Contains(ua, s) }

	switch {
	case has("iPhone") || has("iPod"):
		out.Device, out.OS = "iPhone", "iOS"
	case has("iPad"):
		out.Device, out.OS = "iPad", "iPadOS"
	case has("Android"):
		out.OS = "Android"
		if has("Mobile") {
			out.Device = "Android phone"
		} else {
			out.Device = "Android tablet"
		}
	case has("CrOS"):
		out.Device, out.OS = "Chromebook", "ChromeOS"
	case has("Macintosh") || has("Mac OS X"):
		out.Device, out.OS = "Mac", "macOS"
	case has("Windows"):
		out.Device, out.OS = "Windows PC", "Windows"
	case has("Linux") || has("X11"):
		out.Device, out.OS = "Linux PC", "Linux"
	}

	switch {
	case has("Edg/") || has("EdgiOS/") || has("EdgA/"):
		out.Browser = "Edge"
	case has("OPR/") || has("Opera"):
		out.Browser = "Opera"
	case has("SamsungBrowser/"):
		out.Browser = "Samsung Internet"
	case has("Vivaldi/"):
		out.Browser = "Vivaldi"
	case has("Brave/"):
		out.Browser = "Brave"
	case has("Firefox/") || has("FxiOS/"):
		out.Browser = "Firefox"
	case has("CriOS/") || has("Chrome/") || has("Chromium/"):
		out.Browser = "Chrome"
	case has("Safari/") && (has("Version/") || out.OS == "iOS" || out.OS == "iPadOS"):
		out.Browser = "Safari"
	case strings.HasPrefix(ua, "curl/"):
		out.Browser, out.Device = "curl", "Command line"
	case strings.HasPrefix(ua, "Wget/"):
		out.Browser, out.Device = "Wget", "Command line"
	case strings.HasPrefix(ua, "Go-http-client/"):
		out.Browser, out.Device = "Go client", "Command line"
	}

	// iPadOS 13+ Safari reports itself as a Mac; touch support can't be
	// seen from the header, so it stays "Mac" (as Apple intends).
	return out
}
