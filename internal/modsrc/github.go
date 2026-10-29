package modsrc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAPI      = "https://api.github.com"
	defaultCodeload = "https://codeload.github.com"
	maxRedirects    = 5
	timeout         = 60 * time.Second
)

// Client fetches public repositories from GitHub. It talks to two hosts, the
// API (to turn a ref into a commit) and codeload (for the archive of that
// commit), follows redirects only between them, and reads no more than
// MaxDownload bytes. The address fields exist so a test can point it at a
// local server; nothing in the editor sets them.
type Client struct {
	Transport     http.RoundTripper // nil for the default
	API, Codeload string
}

// NewClient returns a client for github.com.
func NewClient() *Client { return &Client{API: defaultAPI, Codeload: defaultCodeload} }

func (c *Client) base(raw string, fallback string) string {
	if raw == "" {
		raw = fallback
	}
	return strings.TrimRight(raw, "/")
}

func (c *Client) hosts() (map[string]bool, string) {
	allowed := map[string]bool{}
	scheme := ""
	for _, raw := range []string{c.base(c.API, defaultAPI), c.base(c.Codeload, defaultCodeload)} {
		if u, err := url.Parse(raw); err == nil {
			allowed[u.Host] = true
			scheme = u.Scheme
		}
	}
	return allowed, scheme
}

// redirectPolicy lets a redirect go to either GitHub host, over the scheme the
// request began with, and stops a chain that does not end.
func (c *Client) redirectPolicy() func(*http.Request, []*http.Request) error {
	allowed, scheme := c.hosts()
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("too many redirects")
		}
		if !allowed[req.URL.Host] || req.URL.Scheme != scheme || req.URL.User != nil {
			return fmt.Errorf("GitHub redirected to %s, which is not a GitHub download host", req.URL.Host)
		}
		return nil
	}
}

func (c *Client) get(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "coigue")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	hc := http.Client{Transport: c.Transport, CheckRedirect: c.redirectPolicy(), Timeout: timeout}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNotFound:
			return nil, errors.New("not found on GitHub: check the name and the ref (only public repositories can be fetched)")
		case http.StatusForbidden, http.StatusTooManyRequests:
			return nil, fmt.Errorf("GitHub refused the request (%s); it may be rate limiting, try again later", resp.Status)
		}
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return resp, nil
}

// Resolve turns src's ref into the full commit hash it names now. An empty ref
// is the repository's default branch. A ref that is already a commit hash is
// returned as it is, without asking GitHub.
func (c *Client) Resolve(ctx context.Context, src Source) (string, error) {
	if ref := strings.ToLower(src.Ref); IsCommit(ref) {
		return ref, nil
	}
	segments := strings.Split(src.Ref, "/")
	if src.Ref == "" {
		segments = []string{"HEAD"}
	}
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/commits/%s", c.base(c.API, defaultAPI),
		url.PathEscape(src.Owner), url.PathEscape(src.Repo), strings.Join(segments, "/"))
	resp, err := c.get(ctx, endpoint, "application/vnd.github.sha")
	if err != nil {
		return "", fmt.Errorf("%s: %w", src, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", fmt.Errorf("%s: %w", src, err)
	}
	commit := strings.TrimSpace(string(body))
	if !IsCommit(commit) {
		return "", fmt.Errorf("%s: GitHub did not answer with a commit hash", src)
	}
	return commit, nil
}

type limitedBody struct {
	r    io.Reader
	left int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, fmt.Errorf("the download is larger than %d MiB", MaxDownload>>20)
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}

// Fetch downloads the archive of the commit and returns its modules. commit is
// a full hash from Resolve.
func (c *Client) Fetch(ctx context.Context, src Source, commit string) ([]File, error) {
	if !IsCommit(commit) {
		return nil, fmt.Errorf("%q is not a full commit hash", commit)
	}
	endpoint := fmt.Sprintf("%s/%s/%s/tar.gz/%s", c.base(c.Codeload, defaultCodeload),
		url.PathEscape(src.Owner), url.PathEscape(src.Repo), commit)
	resp, err := c.get(ctx, endpoint, "")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	defer resp.Body.Close()
	files, err := Extract(&limitedBody{r: resp.Body, left: MaxDownload})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	return files, nil
}
