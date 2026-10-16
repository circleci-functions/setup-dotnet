package nuget

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigName is the file Authenticate writes into the user's NuGet config
// directory.
const ConfigName = "setup-dotnet.config"

// Source is a package source to authenticate.
type Source struct {
	URL      string
	Username string
	Token    string
}

// UserConfigDir is the directory NuGet merges every *.config in on top of
// the user's NuGet.Config.
func UserConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(appData, "NuGet", "config"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nuget", "NuGet", "config"), nil
}

// FindConfig returns the nuget.config in dir under any of the spellings NuGet
// accepts, or "".
func FindConfig(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), "nuget.config") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// Authenticate writes a NuGet config into configDir that gives src's
// credentials to every source in existing whose URL contains src.URL, or adds
// src as a source named "Source" when none does. src.URL also becomes the
// default push source. existing may be "" or name a file that does not exist.
func Authenticate(configDir, existing string, src Source) (string, error) {
	keys, err := matchingKeys(existing, src.URL)
	if err != nil {
		return "", err
	}
	doc := configuration{Config: &addList{Adds: []add{{Key: "defaultPushSource", Value: src.URL}}}}
	if len(keys) == 0 {
		keys = []string{"Source"}
		doc.PackageSources = &addList{Adds: []add{{Key: "Source", Value: src.URL}}}
	}
	creds := &credentials{}
	for _, k := range keys {
		creds.Sources = append(creds.Sources, credential{
			XMLName: xml.Name{Local: EncodeKey(k)},
			Adds:    []add{{Key: "Username", Value: src.Username}, {Key: "ClearTextPassword", Value: src.Token}},
		})
	}
	doc.Credentials = creds

	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	buf.WriteString("\n")

	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(configDir, ConfigName)
	return p, os.WriteFile(p, buf.Bytes(), 0o600)
}

func matchingKeys(existing, url string) ([]string, error) {
	if existing == "" {
		return nil, nil
	}
	data, err := os.ReadFile(existing) //nolint:gosec // the caller names the file
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc configuration
	if err := xml.Unmarshal(data, &doc); err != nil || doc.XMLName.Local != "configuration" {
		return nil, fmt.Errorf("%s is not a valid NuGet.config", existing)
	}
	var keys []string
	if doc.PackageSources != nil {
		for _, a := range doc.PackageSources.Adds {
			if strings.Contains(strings.ToLower(a.Value), strings.ToLower(url)) {
				keys = append(keys, a.Key)
			}
		}
	}
	return keys, nil
}

// EncodeKey turns a source name into the element name NuGet stores its
// credentials under, encoding what XML names cannot hold as _xHHHH_.
func EncodeKey(key string) string {
	var b strings.Builder
	for i, r := range key {
		valid := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(i > 0 && (r == '-' || r == '.' || (r >= '0' && r <= '9')))
		if valid {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, "_x%04X_", r)
	}
	return b.String()
}

type configuration struct {
	XMLName        xml.Name     `xml:"configuration"`
	Config         *addList     `xml:"config"`
	PackageSources *addList     `xml:"packageSources"`
	Credentials    *credentials `xml:"packageSourceCredentials"`
}

type addList struct {
	Adds []add `xml:"add"`
}

type add struct {
	Key   string `xml:"key,attr"`
	Value string `xml:"value,attr"`
}

type credentials struct {
	Sources []credential
}

type credential struct {
	XMLName xml.Name
	Adds    []add `xml:"add"`
}
