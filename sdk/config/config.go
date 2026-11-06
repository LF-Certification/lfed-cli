package config

import (
	"fmt"
	"os"
	"strings"
)

func Context() (string, error) {
	return require("LFED_CONTEXT")
}

func APIBaseURL() (string, error) {
	v, err := require("LFED_API_BASE_URL")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(v, "/"), nil
}

func Debug() bool { return os.Getenv("LFED_DEBUG") != "" }

func require(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("%s not set; run \"lfed context use <name>\" or run this plugin through \"lfed <plugin>\"", name)
	}
	return v, nil
}
