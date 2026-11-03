package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const cpanelLoginFixture = `<title>cPanel Login</title><form id="login_form" action="/login/"><input name="user"><input name="pass" type="password"></form>`

func TestLoginFormKinds(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"ordinary", loginPage, "admin"},
		{"branded login", `<title>Acme</title><form><input name="username"><input type="password"></form>`, "admin"},
		{"cpanel", cpanelLoginFixture, "cpanel"},
		{"cpanel article", `<title>cPanel help</title><p>cPanel login instructions</p><form><input type="email"></form>`, ""},
		{"script text", `<title>Login</title><script>var html='<form><input type="password"></form>';</script>`, ""},
		{"password outside form", `<title>Admin Login</title><input type="password">`, ""},
		{"password reset", `<title>Reset password</title><form><input name="user"><input type="password"></form>`, ""},
		{"registration", `<title>Create an account</title><form><input name="email"><input type="password"></form>`, ""},
		{"new password", `<title>Acme</title><form><input name="username"><input type="password" autocomplete="new-password"></form>`, ""},
		{"cpanel translation", `<title>cPanel Iniciar sesión</title><form id="login_form" action="/login/"><input name="user"><input type="password"></form>`, "cpanel"},
		{"soft404", `<title>404 Not found</title><form><input name="user"><input type="password"></form>`, ""},
		{"wordpress", `<title>Log In</title><form action="/wp-login.php"><input type="password"></form>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := loginFormKind([]byte(tc.body)); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestPipelineSeparatesCPanelFromOrdinaryAdmin(t *testing.T) {
	for _, kind := range []string{"cpanel-home", "cpanel-path", "admin"} {
		t.Run(kind, func(t *testing.T) {
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "cpanel-home" || r.URL.Path == hardcodedAdminPaths[0] {
					if kind == "admin" {
						fmt.Fprint(w, loginPage)
					} else {
						fmt.Fprint(w, cpanelLoginFixture)
					}
					return
				}
				http.NotFound(w, r)
			}))
			defer fixture.Close()
			options := pipelineFixture(t, fixture.URL, nil)
			var out, log bytes.Buffer
			if code := runVersionPipeline(context.Background(), options, &out, &log); code != 0 {
				t.Fatalf("code=%d %s", code, &log)
			}
			got := out.String()
			cpanel := strings.Count(got, "[FILE:cpanel.txt]")
			admin := strings.Count(got, "[FILE:admin.txt]")
			if kind == "admin" && (admin != 1 || cpanel != 0) {
				t.Fatalf("ordinary login misrouted: %s", got)
			}
			if kind != "admin" && (cpanel != 1 || admin != 0 || strings.Contains(got, "[FILE:enum-admin.txt]")) {
				t.Fatalf("cPanel mixed with ordinary admin: %s", got)
			}
			if strings.Contains(got, ".jsonl") {
				t.Fatal("internal JSONL exported")
			}
		})
	}
}
