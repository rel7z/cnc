package main

import (
	"golang.org/x/net/html"
	"regexp"
	"strings"
)

// Require actual form/input elements, not password-looking text in scripts or
// articles. cPanel additionally requires its login branding and form evidence.
func loginFormKind(body []byte) string {
	for _, signatures := range [][]*regexp.Regexp{wpFingerprints, joomlaFingerprints} {
		for _, signature := range signatures {
			if signature.Match(body) {
				return ""
			}
		}
	}
	type form struct{ password, newPassword, identity, loginAction, cpanelID bool }
	forms := []form{}
	current := -1
	inTitle := false
	title := ""
	cpanelAsset := false
	z := html.NewTokenizer(strings.NewReader(string(body)))
	for {
		tokenType := z.Next()
		if tokenType == html.ErrorToken {
			break
		}
		if tokenType == html.TextToken && inTitle {
			title += string(z.Text())
			continue
		}
		if tokenType == html.EndTagToken {
			token := z.Token()
			if token.Data == "title" {
				inTitle = false
			}
			if token.Data == "form" {
				current = -1
			}
			continue
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
			continue
		}
		token := z.Token()
		attrs := map[string]string{}
		for _, attr := range token.Attr {
			attrs[attr.Key] = strings.ToLower(attr.Val)
		}
		if token.Data == "title" {
			inTitle = true
		}
		if (token.Data == "img" || token.Data == "link" || token.Data == "script") &&
			((strings.Contains(attrs["src"], "/unprotected/cpanel/") && strings.Contains(attrs["src"], "cpanel-logo")) ||
				(strings.Contains(attrs["href"], "/unprotected/cpanel/") && strings.Contains(attrs["href"], "cpanel-logo"))) {
			cpanelAsset = true
		}
		if token.Data == "form" {
			action := attrs["action"]
			forms = append(forms, form{loginAction: strings.Contains(action, "login") || strings.Contains(action, "signin") || strings.Contains(action, "auth") || strings.Contains(action, "session"), cpanelID: attrs["id"] == "login_form"})
			current = len(forms) - 1
		}
		if token.Data == "input" && current >= 0 {
			forms[current].password = forms[current].password || attrs["type"] == "password"
			forms[current].newPassword = forms[current].newPassword || attrs["autocomplete"] == "new-password"
			switch attrs["name"] {
			case "user", "username", "email", "login", "log":
				forms[current].identity = true
			}
			forms[current].identity = forms[current].identity || attrs["type"] == "email"
		}
	}
	title = strings.ToLower(strings.Join(strings.Fields(title), " "))
	for _, bad := range []string{"404", "not found", "doesn't exist", "forgot password", "reset password", "change password", "sign up", "signup", "register", "registration", "create account", "create an account"} {
		if strings.Contains(title, bad) {
			return ""
		}
	}
	loginTitle := false
	for _, word := range []string{"login", "log in", "sign in", "sign-in", "admin", "auth", "panel", "dashboard", "portal"} {
		loginTitle = loginTitle || strings.Contains(title, word)
	}
	for _, f := range forms {
		if !f.password || f.newPassword {
			continue
		}
		if f.identity && (cpanelAsset || (strings.Contains(title, "cpanel") && f.cpanelID && f.loginAction)) {
			return "cpanel"
		}
	}
	for _, f := range forms {
		if f.password && !f.newPassword && (f.identity || f.loginAction || loginTitle) {
			return "admin"
		}
	}
	return ""
}
