package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/suzuki-shunsuke/saki/pkg/config"
)

func TestLocate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		want    *config.Location
		wantErr bool
	}{
		{
			name: "SAKI_CONFIG wins",
			env:  map[string]string{"SAKI_CONFIG": "/a/b.yaml", "XDG_CONFIG_HOME": "/xdg", "HOME": "/home/foo"},
			want: &config.Location{Path: "/a/b.yaml", Explicit: true},
		},
		{
			name: "XDG_CONFIG_HOME",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/home/foo"},
			want: &config.Location{Path: filepath.Join("/xdg", "saki", "saki.yaml")},
		},
		{
			name: "HOME",
			env:  map[string]string{"HOME": "/home/foo"},
			want: &config.Location{Path: filepath.Join("/home/foo", ".config", "saki", "saki.yaml")},
		},
		{
			name: "empty values are unset",
			env:  map[string]string{"SAKI_CONFIG": "", "XDG_CONFIG_HOME": "", "HOME": "/home/foo"},
			want: &config.Location{Path: filepath.Join("/home/foo", ".config", "saki", "saki.yaml")},
		},
		{
			name:    "nothing is set",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := config.Locate(func(k string) string { return tt.env[k] })
			if tt.wantErr {
				if err == nil {
					t.Fatal("an error should be returned")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestRead(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    *config.Config
		wantErr bool
	}{
		{name: "assignee", content: "assignee: octocat\n", want: &config.Config{Assignee: "octocat"}},
		{name: "unknown keys are ignored", content: "assignee: octocat\nfoo: bar\n", want: &config.Config{Assignee: "octocat"}},
		{name: "empty file", content: "", want: &config.Config{}},
		{name: "invalid YAML", content: "assignee: [\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "saki.yaml")
			if err := os.WriteFile(p, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := config.Read(p)
			if tt.wantErr {
				if err == nil {
					t.Fatal("an error should be returned")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func TestRead_notExist(t *testing.T) {
	t.Parallel()
	_, err := config.Read(filepath.Join(t.TempDir(), "saki.yaml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the error should wrap fs.ErrNotExist: %v", err)
	}
}
