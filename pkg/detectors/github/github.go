package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/trufflesecurity/trufflehog/v3/pkg/detectors"
	"github.com/trufflesecurity/trufflehog/v3/pkg/pb/detectorspb"

	"github.com/trufflesecurity/trufflehog/v3/pkg/common"
)

const defaultURL = "https://api.github.com"

type Scanner struct {
	verifierURLs []string
}

// New creates a new Scanner with the given options.
func New(opts ...func(*Scanner)) *Scanner {
	scanner := &Scanner{
		verifierURLs: make([]string, 0),
	}
	for _, opt := range opts {
		opt(scanner)
	}

	return scanner
}

// WithVerifierURLs adds the given URLs to the list of URLs to check for
// verification of secrets.
func WithVerifierURLs(urls []string, includeDefault bool) func(*Scanner) {
	return func(s *Scanner) {
		if includeDefault {
			urls = append(urls, defaultURL)
		}
		s.verifierURLs = append(s.verifierURLs, urls...)
	}
}

// Ensure the Scanner satisfies the interfaces at compile time.
var _ detectors.Detector = (*Scanner)(nil)
var _ detectors.Versioner = (*Scanner)(nil)

func (s Scanner) Version() int { return 2 }

var (
	// Oauth token
	// https://developer.github.com/v3/#oauth2-token-sent-in-a-header
	// Token type list:
	// https://github.blog/2021-04-05-behind-githubs-new-authentication-token-formats/
	// https://github.blog/changelog/2022-10-18-introducing-fine-grained-personal-access-tokens/
	keyPat = regexp.MustCompile(`\b((?:ghp|gho|ghu|ghs|ghr|github_pat)_[a-zA-Z0-9_]{36,255})\b`)

	// TODO: Oauth2 client_id and client_secret
	// https://developer.github.com/v3/#oauth2-keysecret
)

// TODO: Add secret context?? Information about access, ownership etc
type userRes struct {
	Login     string `json:"login"`
	Type      string `json:"type"`
	SiteAdmin bool   `json:"site_admin"`
	Name      string `json:"name"`
	Company   string `json:"company"`
	UserURL   string `json:"html_url"`
}

// Keywords are used for efficiently pre-filtering chunks.
// Use identifiers in the secret preferably, or the provider name.
func (s Scanner) Keywords() []string {
	return []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"}
}

// FromData will find and optionally verify GitHub secrets in a given set of bytes.
func (s Scanner) FromData(ctx context.Context, verify bool, data []byte) (results []detectors.Result, err error) {
	dataStr := string(data)

	matches := keyPat.FindAllStringSubmatch(dataStr, -1)

	for _, match := range matches {
		// First match is entire regex, second is the first group.
		if len(match) != 2 {
			continue
		}

		token := match[1]

		s1 := detectors.Result{
			DetectorType: detectorspb.DetectorType_Github,
			Raw:          []byte(token),
		}

		if verify {
			client := common.SaneHttpClient()
			// https://developer.github.com/v3/users/#get-the-authenticated-user
			for _, url := range s.verifierURLs {
				req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/user", url), nil)
				if err != nil {
					continue
				}
				req.Header.Add("Content-Type", "application/json; charset=utf-8")
				req.Header.Add("Authorization", fmt.Sprintf("token %s", token))
				res, err := client.Do(req)
				if err == nil {
					if res.StatusCode >= 200 && res.StatusCode < 300 {
						var userResponse userRes
						err = json.NewDecoder(res.Body).Decode(&userResponse)
						res.Body.Close()
						if err == nil {
							s1.Verified = true
							s1.ExtraData = map[string]string{
								"username":     userResponse.Login,
								"url":          userResponse.UserURL,
								"account_type": userResponse.Type,
								"site_admin":   fmt.Sprintf("%t", userResponse.SiteAdmin),
								"name":         userResponse.Name,
								"company":      userResponse.Company,
							}
						}
					}
				}
			}
		}

		if !s1.Verified && detectors.IsKnownFalsePositive(string(s1.Raw), detectors.DefaultFalsePositives, true) {
			continue
		}

		results = append(results, s1)
	}

	return
}

func (s Scanner) Type() detectorspb.DetectorType {
	return detectorspb.DetectorType_Github
}
