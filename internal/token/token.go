// Package token resolves the HCP Terraform API token from the same sources
// the terraform CLI uses, in the same priority order as `terraform login`
// consumers expect:
//
//  1. TF_TOKEN_app_terraform_io environment variable
//  2. the file pointed to by TFC_CREDENTIALS_FILE
//  3. ~/.terraform.d/credentials.tfrc.json (the default `terraform login` location)
package token

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const envToken = "TF_TOKEN_app_terraform_io"
const envCredentialsFile = "TFC_CREDENTIALS_FILE"

// Host is the HCP Terraform hostname whose credentials this package resolves.
const Host = "app.terraform.io"

// NotFoundError is returned when no token could be resolved from any source.
type NotFoundError struct {
	CredentialsFile string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("HCP Terraform のトークンが見つかりません。`terraform login` を実行してください (期待するパス: %s)", e.CredentialsFile)
}

type credentialsFile struct {
	Credentials map[string]struct {
		Token string `json:"token"`
	} `json:"credentials"`
}

// Resolve returns the HCP Terraform API token, checking the environment
// variable, then the credentials file, in priority order.
func Resolve() (string, error) {
	if t := os.Getenv(envToken); t != "" {
		return t, nil
	}

	path := os.Getenv(envCredentialsFile)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("ホームディレクトリを解決できません: %w", err)
		}
		path = filepath.Join(home, ".terraform.d", "credentials.tfrc.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &NotFoundError{CredentialsFile: path}
		}
		return "", fmt.Errorf("%s の読み込みに失敗しました: %w", path, err)
	}

	var creds credentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", fmt.Errorf("%s のパースに失敗しました: %w", path, err)
	}

	entry, ok := creds.Credentials[Host]
	if !ok || entry.Token == "" {
		return "", fmt.Errorf("%s に %s のトークンがありません。`terraform login` を実行してください", path, Host)
	}

	return entry.Token, nil
}
