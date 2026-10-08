package store

import (
	"strings"
	"testing"
)

func TestParseRepoURL(t *testing.T) {
	for raw, want := range map[string][3]string{ // provider, repo, web URL
		"https://github.com/CesarPetrescu/ledger":       {"github", "CesarPetrescu/ledger", "https://github.com/CesarPetrescu/ledger"},
		"https://github.com/CesarPetrescu/ledger.git/":  {"github", "CesarPetrescu/ledger", "https://github.com/CesarPetrescu/ledger"},
		"git@github.com:CesarPetrescu/ledger.git":       {"github", "CesarPetrescu/ledger", "https://github.com/CesarPetrescu/ledger"},
		"ssh://git@www.GitHub.com/CesarPetrescu/ledger": {"github", "CesarPetrescu/ledger", "https://github.com/CesarPetrescu/ledger"},
		"https://gitlab.com/group/sub/app":              {"gitlab", "group/sub/app", "https://gitlab.com/group/sub/app"},
		"https://codeberg.org/me/tool.git":              {"forgejo", "me/tool", "https://codeberg.org/me/tool"},
		"http://forgejo.lan:3000/photon/panel.git":      {"git", "photon/panel", "http://forgejo.lan:3000/photon/panel"},
		"ssh://git@192.168.10.29:2222/photon/panel.git": {"git", "photon/panel", ""},
		"github-personal:CesarPetrescu/ledger.git":      {"git", "CesarPetrescu/ledger", ""},
		"git@forgejo.lan:/srv/git/panel.git":            {"git", "srv/git/panel", ""},
	} {
		got, err := ParseRepoURL(raw)
		if err != nil || got.Provider != want[0] || got.Repo != want[1] || got.WebURL != want[2] {
			t.Errorf("%s = %+v, %v", raw, got, err)
		}
	}
	// The same repository by HTTPS and by SSH is one link.
	a, _ := ParseRepoURL("https://github.com/CesarPetrescu/Ledger")
	b, _ := ParseRepoURL("git@github.com:cesarpetrescu/ledger.git")
	if a.Key != b.Key {
		t.Errorf("keys differ: %q %q", a.Key, b.Key)
	}
	// A default port is the same server; another port is another server.
	for _, same := range [][2]string{{"https://github.com:443/acme/app", "ssh://git@github.com:22/acme/app"}, {"http://forgejo.lan:80/acme/app", "http://forgejo.lan/acme/app"}} {
		x, _ := ParseRepoURL(same[0])
		y, _ := ParseRepoURL(same[1])
		if x.Key != y.Key {
			t.Errorf("%s and %s differ: %q %q", same[0], same[1], x.Key, y.Key)
		}
	}
	c, _ := ParseRepoURL("http://forgejo.lan:3000/acme/app")
	d, _ := ParseRepoURL("http://forgejo.lan:4000/acme/app")
	if c.Key == d.Key || c.Key != "forgejo.lan:3000/acme/app" {
		t.Errorf("ports collapsed: %q %q", c.Key, d.Key)
	}
	for raw, why := range map[string]string{
		"https://ghp_secret@github.com/owner/repo": "user name",
		"https://user:token@github.com/owner/repo": "user name",
		"ssh://git:hunter2@host/owner/repo":        "password",
		"https://github.com/owner/repo?token=x":    "query",
		"https://github.com/owner":                 "OWNER/REPO",
		"https://github.com/owner/repo/tree/main":  "OWNER/REPO",
		"https://github.com/owner/repo%3Fx":        "OWNER/REPO",
		"ftp://host/owner/repo":                    "https, http, ssh",
		"https://host/owner/../etc":                "repository path",
		"C:\\repos\\ledger":                        "",
		"owner/repo":                               "",
		"https://github.com/owner/repo with space": "",
		"": "",
	} {
		if _, err := ParseRepoURL(raw); err == nil || why != "" && !strings.Contains(err.Error(), why) {
			t.Errorf("%q: %v (want %q)", raw, err, why)
		}
	}
}
