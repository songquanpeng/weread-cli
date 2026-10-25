package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Config struct{ Home, DB string }

var ErrInvalidCredentials = errors.New("invalid auth.json; run weread-cli login")

func Load(home, db string) (Config, error) {
	if home == "" {
		home = os.Getenv("WEREAD_HOME")
	}
	if home == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return Config{}, err
		}
		home = filepath.Join(dir, "weread-cli")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return Config{}, err
	}
	if db == "" {
		db = os.Getenv("WEREAD_DB")
	}
	if db == "" {
		db = filepath.Join(home, "weread.db")
	}
	db, err = filepath.Abs(db)
	return Config{Home: home, DB: db}, err
}

func (c Config) Ensure() error {
	for _, p := range []string{c.Home, filepath.Dir(c.DB)} {
		if err := os.MkdirAll(p, 0700); err != nil {
			return err
		}
	}
	return os.Chmod(c.Home, 0700)
}

func (c Config) AuthPath() string { return filepath.Join(c.Home, "auth.json") }

func (c Config) Key() (string, string, error) {
	b, err := os.ReadFile(c.AuthPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	if err == nil {
		var v struct {
			Key string `json:"api_key"`
		}
		if json.Unmarshal(b, &v) != nil {
			return "", "", ErrInvalidCredentials
		}
		if key := strings.TrimSpace(v.Key); key != "" {
			if strings.ContainsAny(key, "\r\n\t ") {
				return "", "", ErrInvalidCredentials
			}
			return key, "file", nil
		}
	}
	if key := strings.TrimSpace(os.Getenv("WEREAD_API_KEY")); key != "" {
		return key, "environment", nil
	}
	return "", "none", nil
}

func (c Config) SaveKey(key string) error {
	if key = strings.TrimSpace(key); key == "" || strings.ContainsAny(key, "\r\n\t ") {
		return errors.New("API key must be a nonempty single token")
	}
	if err := c.Ensure(); err != nil {
		return err
	}
	b, _ := json.Marshal(struct {
		Key string `json:"api_key"`
	}{key})
	f, err := os.CreateTemp(c.Home, ".auth-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), c.AuthPath())
}
