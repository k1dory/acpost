package forge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	listMax = 4 << 20 // a list's answer
	logMax  = 8 << 20 // a job's log read
	pageLen = 50
)

// glProject is a GitLab project's id in a path: its full path, escaped whole.
func glProject(path string) string { return strings.ReplaceAll(url.PathEscape(path), "/", "%2F") }

// Pulls lists a repository's pull requests: state open, closed or all.
func (c *Client) Pulls(ctx context.Context, srv Server, r Repo, state string) ([]Pull, error) {
	l, err := c.PullList(ctx, srv, r, state)
	return l.Items, err
}

// PullList is a list of pull requests, and whether the forge has more than it holds.
type PullList struct {
	Items     []Pull `json:"items"`
	Truncated bool   `json:"truncated,omitempty"`
}

// PullList lists a repository's pull requests a page at a time, up to listPages pages: past
// them the list says it is cut.
func (c *Client) PullList(ctx context.Context, srv Server, r Repo, state string) (PullList, error) {
	return c.listPulls(ctx, srv, r, state, nil)
}

// listPulls lists pull requests of a state, filter added to the query (head, source_branch…).
func (c *Client) listPulls(ctx context.Context, srv Server, r Repo, state string, filter url.Values) (PullList, error) {
	if state == "" {
		state = "open"
	}
	var out PullList
	switch srv.Kind {
	case GitHub, Forgejo:
		q := url.Values{"state": {state}}
		per := "per_page"
		if srv.Kind == Forgejo {
			per = "limit"
		}
		q.Set(per, strconv.Itoa(pageLen))
		for k, v := range filter {
			q[k] = v
		}
		var err error
		out.Truncated, err = c.pages(ctx, srv, r.Host, "/repos/"+r.Path+"/pulls", q, pageLen, func(get func(any) error) (int, error) {
			var list []struct {
				Number  int    `json:"number"`
				Title   string `json:"title"`
				State   string `json:"state"`
				Draft   bool   `json:"draft"`
				HTMLURL string `json:"html_url"`
				User    struct {
					Login string `json:"login"`
				} `json:"user"`
				Head struct {
					Ref  string `json:"ref"`
					Repo *struct {
						FullName string `json:"full_name"`
					} `json:"repo"` // null: the fork is gone
				} `json:"head"`
				Base struct {
					Ref string `json:"ref"`
				} `json:"base"`
				Updated  time.Time  `json:"updated_at"`
				MergedAt *time.Time `json:"merged_at"`
				Merged   bool       `json:"merged"`
			}
			if err := get(&list); err != nil {
				return 0, err
			}
			for _, p := range list {
				st := p.State
				if p.Merged || p.MergedAt != nil {
					st = "merged"
				}
				pr := Pull{Number: p.Number, Title: p.Title, State: st, Draft: p.Draft, Author: p.User.Login,
					Head: p.Head.Ref, Base: p.Base.Ref, URL: p.HTMLURL, Updated: p.Updated}
				if p.Head.Repo != nil {
					pr.HeadRepo = p.Head.Repo.FullName
				}
				out.Items = append(out.Items, pr)
			}
			return len(list), nil
		})
		if err != nil {
			return PullList{}, err
		}
	case GitLab:
		gl := map[string]string{"open": "opened", "closed": "closed", "merged": "merged", "all": "all"}[state]
		if gl == "" {
			gl = "opened"
		}
		q := url.Values{"state": {gl}, "per_page": {strconv.Itoa(pageLen)}}
		for k, v := range filter {
			q[k] = v
		}
		var err error
		out.Truncated, err = c.pages(ctx, srv, r.Host, "/projects/"+glProject(r.Path)+"/merge_requests", q, pageLen, func(get func(any) error) (int, error) {
			var list []struct {
				IID          int    `json:"iid"`
				Title        string `json:"title"`
				State        string `json:"state"`
				Draft        bool   `json:"draft"`
				WebURL       string `json:"web_url"`
				SourceBranch string `json:"source_branch"`
				TargetBranch string `json:"target_branch"`
				SourceProj   int64  `json:"source_project_id"`
				TargetProj   int64  `json:"target_project_id"`
				Author       struct {
					Username string `json:"username"`
				} `json:"author"`
				Updated time.Time `json:"updated_at"`
			}
			if err := get(&list); err != nil {
				return 0, err
			}
			for _, m := range list {
				st := map[string]string{"opened": "open"}[m.State]
				if st == "" {
					st = m.State
				}
				pr := Pull{Number: m.IID, Title: m.Title, State: st, Draft: m.Draft, Author: m.Author.Username,
					Head: m.SourceBranch, Base: m.TargetBranch, URL: m.WebURL, Updated: m.Updated, HeadRepo: r.Path}
				if m.SourceProj != m.TargetProj {
					pr.HeadRepo = "project " + strconv.FormatInt(m.SourceProj, 10) // a fork's: its path is not in the list
				}
				out.Items = append(out.Items, pr)
			}
			return len(list), nil
		})
		if err != nil {
			return PullList{}, err
		}
	default:
		return PullList{}, fmt.Errorf("forge kind %q", srv.Kind)
	}
	return out, nil
}

// listPages is how many pages a list reads at most.
const listPages = 10

// pages reads a list a page at a time (page=1, 2…; q holds the page size, size): each page is
// given to read, which says how many items it had. It goes on while the forge says there is a next
// page (GitHub and Forgejo a Link rel="next", GitLab X-Next-Page; a full page when it says
// neither), up to listPages: cut there, it says so.
func (c *Client) pages(ctx context.Context, srv Server, host, path string, q url.Values, size int, read func(get func(any) error) (int, error)) (bool, error) {
	for page := 1; ; page++ {
		q.Set("page", strconv.Itoa(page))
		var h http.Header
		n, err := read(func(out any) error {
			var err error
			h, err = c.get(ctx, srv, host, path+"?"+q.Encode(), out, listMax)
			return err
		})
		if err != nil {
			return false, err
		}
		if !hasNext(h, n, size) {
			return false, nil
		}
		if page == listPages {
			return true, nil
		}
	}
}

// hasNext says whether a page is followed by another.
func hasNext(h http.Header, n, size int) bool {
	if l := h.Get("Link"); l != "" {
		return strings.Contains(l, `rel="next"`)
	}
	if _, ok := h["X-Next-Page"]; ok {
		return h.Get("X-Next-Page") != ""
	}
	return n >= size
}

// PullRequest opens a pull request.
type PullRequest struct {
	Head  string `json:"head"`
	Base  string `json:"base"`
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	Draft bool   `json:"draft,omitempty"`
}

// OpenPull opens a pull (merge) request.
func (c *Client) OpenPull(ctx context.Context, srv Server, r Repo, p PullRequest) (Pull, error) {
	if p.Head == "" || p.Base == "" || p.Title == "" {
		return Pull{}, fmt.Errorf("a pull request needs head, base and title")
	}
	switch srv.Kind {
	case GitHub, Forgejo:
		var got struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
			State   string `json:"state"`
		}
		title := p.Title
		if p.Draft && srv.Kind == Forgejo {
			title = "WIP: " + title // Forgejo and Gitea: a draft is a title with this prefix
		}
		body := map[string]any{"head": p.Head, "base": p.Base, "title": title, "body": p.Body}
		if srv.Kind == GitHub {
			body["draft"] = p.Draft
		}
		if _, err := c.do(ctx, "POST", srv, r.Host, "/repos/"+r.Path+"/pulls", body, &got, listMax); err != nil {
			return c.alreadyOpen(ctx, srv, r, p, err)
		}
		return Pull{Number: got.Number, Title: title, State: "open", Draft: p.Draft, Head: p.Head, Base: p.Base, URL: got.HTMLURL}, nil
	case GitLab:
		title := p.Title
		if p.Draft {
			title = "Draft: " + title
		}
		var got struct {
			IID    int    `json:"iid"`
			WebURL string `json:"web_url"`
		}
		body := map[string]any{"source_branch": p.Head, "target_branch": p.Base, "title": title, "description": p.Body}
		if _, err := c.do(ctx, "POST", srv, r.Host, "/projects/"+glProject(r.Path)+"/merge_requests", body, &got, listMax); err != nil {
			return c.alreadyOpen(ctx, srv, r, p, err)
		}
		return Pull{Number: got.IID, Title: title, State: "open", Draft: p.Draft, Head: p.Head, Base: p.Base, URL: got.WebURL}, nil
	}
	return Pull{}, fmt.Errorf("forge kind %q", srv.Kind)
}

// Whoami is the login a host's token is (the token checked against the forge).
func (c *Client) Whoami(ctx context.Context, srv Server, host string) (string, error) {
	var u struct {
		Login    string `json:"login"`    // GitHub, Forgejo/Gitea
		Username string `json:"username"` // GitLab
	}
	if _, err := c.get(ctx, srv, host, "/user", &u, listMax); err != nil {
		return "", err
	}
	return cmpOr(u.Login, u.Username), nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Merge is how a pull request is merged.
type Merge struct {
	Number int    `json:"number"`
	Method string `json:"method,omitempty"` // merge (default), squash, rebase
	// SHA, when given, is the head the merge must be of: a pull request pushed to since it was
	// looked at is not merged.
	SHA string `json:"sha,omitempty"`
}

// MergePull merges a pull (merge) request.
func (c *Client) MergePull(ctx context.Context, srv Server, r Repo, m Merge) error {
	if m.Number <= 0 {
		return fmt.Errorf("a merge needs the pull request's number")
	}
	if m.Method == "" {
		m.Method = "merge"
	}
	if m.Method != "merge" && m.Method != "squash" && m.Method != "rebase" {
		return fmt.Errorf("merge method %q: merge, squash or rebase", m.Method)
	}
	num := strconv.Itoa(m.Number)
	switch srv.Kind {
	case GitHub:
		body := map[string]any{"merge_method": m.Method}
		if m.SHA != "" {
			body["sha"] = m.SHA
		}
		_, err := c.do(ctx, "PUT", srv, r.Host, "/repos/"+r.Path+"/pulls/"+num+"/merge", body, nil, listMax)
		return err
	case Forgejo:
		body := map[string]any{"Do": m.Method} // merge, squash, rebase: Forgejo's own names
		if m.SHA != "" {
			body["head_commit_id"] = m.SHA
		}
		_, err := c.do(ctx, "POST", srv, r.Host, "/repos/"+r.Path+"/pulls/"+num+"/merge", body, nil, listMax)
		return err
	case GitLab:
		body := map[string]any{"squash": m.Method == "squash"}
		if m.SHA != "" {
			body["sha"] = m.SHA
		}
		if m.Method == "rebase" {
			return fmt.Errorf("GitLab: rebase is the project's merge method, not a merge's: use merge")
		}
		_, err := c.do(ctx, "PUT", srv, r.Host, "/projects/"+glProject(r.Path)+"/merge_requests/"+num+"/merge", body, nil, listMax)
		return err
	}
	return fmt.Errorf("forge kind %q", srv.Kind)
}

// Checks lists the CI runs of a ref (a branch, a commit), newest first, with their jobs.
func (c *Client) Checks(ctx context.Context, srv Server, r Repo, ref string) ([]Check, error) {
	l, err := c.CheckList(ctx, srv, r, ref)
	return l.Items, err
}

// CheckList is a list of CI runs, and whether the forge has more than it holds (runs: the newest
// few are read; commit statuses: up to listPages pages).
type CheckList struct {
	Items     []Check `json:"items"`
	Truncated bool    `json:"truncated,omitempty"`
}

// CheckList lists the CI runs of a ref (a branch, a commit), newest first, with their jobs.
func (c *Client) CheckList(ctx context.Context, srv Server, r Repo, ref string) (CheckList, error) {
	var out []Check
	cut := false
	switch srv.Kind {
	case GitHub:
		runs, more, err := c.actionsRuns(ctx, srv, r, ref)
		if err != nil {
			return CheckList{}, err
		}
		out, cut = runs, more
	case GitLab:
		var pipes []struct {
			ID      int64     `json:"id"`
			Status  string    `json:"status"`
			Ref     string    `json:"ref"`
			SHA     string    `json:"sha"`
			WebURL  string    `json:"web_url"`
			Created time.Time `json:"created_at"`
			Updated time.Time `json:"updated_at"`
		}
		q := "?per_page=10"
		if ref != "" {
			q += "&ref=" + url.QueryEscape(ref)
		}
		proj := "/projects/" + glProject(r.Path)
		h, err := c.get(ctx, srv, r.Host, proj+"/pipelines"+q, &pipes, listMax)
		if err != nil {
			return CheckList{}, err
		}
		cut = hasNext(h, len(pipes), 10)
		for _, p := range pipes {
			ch := Check{ID: strconv.FormatInt(p.ID, 10), Name: "pipeline #" + strconv.FormatInt(p.ID, 10), Status: glStatus(p.Status),
				URL: p.WebURL, Ref: p.Ref, SHA: p.SHA, Started: p.Created, LogsByJobs: true}
			more, err := c.pages(ctx, srv, r.Host, proj+"/pipelines/"+ch.ID+"/jobs", url.Values{"per_page": {"100"}}, 100, func(get func(any) error) (int, error) {
				var jobs []struct {
					ID       int64      `json:"id"`
					Name     string     `json:"name"`
					Stage    string     `json:"stage"`
					Status   string     `json:"status"`
					WebURL   string     `json:"web_url"`
					Started  *time.Time `json:"started_at"`
					Finished *time.Time `json:"finished_at"`
				}
				if err := get(&jobs); err != nil {
					return 0, err
				}
				for _, j := range jobs {
					job := Job{ID: strconv.FormatInt(j.ID, 10), Name: strings.TrimPrefix(j.Stage+" / "+j.Name, " / "), Status: glStatus(j.Status), URL: j.WebURL}
					if j.Started != nil {
						job.Started = *j.Started
					}
					if j.Finished != nil {
						job.Finished = *j.Finished
					}
					ch.Jobs = append(ch.Jobs, job)
				}
				return len(jobs), nil
			})
			ch.JobsError = jobsError(more, err)
			out = append(out, ch)
		}
	case Forgejo:
		// Gitea 1.24+ has GitHub's Actions API (runs, jobs, a job's log); Forgejo and older Gitea
		// do not: then commit statuses, what Actions (and any CI) report on a commit.
		if runs, more, err := c.actionsRuns(ctx, srv, r, ref); err == nil {
			return CheckList{Items: runs, Truncated: more}, nil
		} else if fe, ok := err.(*Error); !ok || fe.Status != 404 {
			return CheckList{}, err
		}
		if ref == "" {
			return CheckList{}, fmt.Errorf("checks on a Forgejo/Gitea forge need a branch or commit")
		}
		type status struct {
			ID        int64     `json:"id"`
			Context   string    `json:"context"`
			Status    string    `json:"status"`
			TargetURL string    `json:"target_url"`
			Created   time.Time `json:"created_at"`
			Updated   time.Time `json:"updated_at"`
		}
		var st []status
		more, err := c.pages(ctx, srv, r.Host, "/repos/"+r.Path+"/commits/"+url.PathEscape(ref)+"/statuses", url.Values{"limit": {"50"}}, 50, func(get func(any) error) (int, error) {
			var page []status
			if err := get(&page); err != nil {
				return 0, err
			}
			st = append(st, page...)
			return len(page), nil
		})
		if err != nil {
			return CheckList{}, err
		}
		cut = more
		latest := map[string]int{}
		for i, s := range st {
			if j, ok := latest[s.Context]; !ok || s.Updated.After(st[j].Updated) {
				latest[s.Context] = i
			}
		}
		for _, i := range latest {
			s := st[i]
			ch := Check{ID: "status:" + strconv.FormatInt(s.ID, 10), Name: s.Context, Status: fjStatus(s.Status), URL: s.TargetURL, Ref: ref, Started: s.Created}
			if ch.Status != "running" && ch.Status != "queued" {
				ch.Finished = s.Updated
			}
			out = append(out, ch)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	default:
		return CheckList{}, fmt.Errorf("forge kind %q", srv.Kind)
	}
	return CheckList{Items: out, Truncated: cut}, nil
}

// jobsError says why a run's jobs are not all there: an error reading them, or more pages than read.
func jobsError(more bool, err error) string {
	switch {
	case err != nil:
		return "its jobs could not be read: " + err.Error()
	case more:
		return "more jobs than " + strconv.Itoa(listPages) + " pages: the first shown"
	}
	return ""
}

// Log reads a job's log (GitHub, GitLab: a job id; Forgejo/Gitea: a check's id or its target
// URL), at most logMax bytes from its end.
func (c *Client) Log(ctx context.Context, srv Server, r Repo, job string, target string) (string, error) {
	var text tailText
	var err error
	switch srv.Kind {
	case GitHub:
		_, err = c.get(ctx, srv, r.Host, "/repos/"+r.Path+"/actions/jobs/"+url.PathEscape(job)+"/logs", &text, logMax)
	case GitLab:
		_, err = c.get(ctx, srv, r.Host, "/projects/"+glProject(r.Path)+"/jobs/"+url.PathEscape(job)+"/trace", &text, logMax)
	case Forgejo:
		// A job of an Actions run (Gitea 1.24+) has its log over the API; a commit status
		// (Forgejo, older Gitea) has none: the error says where to look.
		noLogs := &Error{Status: 404, Message: fmt.Sprintf("%s (version %s) gives no job logs over its API: open %s",
			r.Host, c.shown(r.Host, srv.Version, 40), c.shown(r.Host, plainURL(target), 300))}
		if strings.HasPrefix(job, "status:") {
			err = noLogs
			break
		}
		_, err = c.get(ctx, srv, r.Host, "/repos/"+r.Path+"/actions/jobs/"+url.PathEscape(job)+"/logs", &text, logMax)
		if fe, ok := err.(*Error); ok && fe.Status == 404 {
			err = noLogs
		}
	default:
		err = fmt.Errorf("forge kind %q", srv.Kind)
	}
	out := strings.TrimPrefix(text.Text, "\ufeff")
	if text.Cut {
		out = "… (the log's start is cut: it is over " + strconv.Itoa(logMax>>20) + " MiB)\n" + out
	}
	return out, err
}

func ghStatus(status, conclusion string) string {
	switch status {
	case "queued", "waiting", "pending", "requested":
		return "queued"
	case "in_progress":
		return "running"
	}
	switch conclusion {
	case "success", "failure", "cancelled", "skipped":
		return conclusion
	case "timed_out", "startup_failure", "action_required":
		return "failure"
	case "neutral":
		return "success"
	}
	return conclusion
}

func glStatus(s string) string {
	switch s {
	case "created", "waiting_for_resource", "preparing", "pending", "scheduled", "manual":
		return "queued"
	case "running":
		return "running"
	case "success", "skipped":
		return s
	case "failed":
		return "failure"
	case "canceled":
		return "cancelled"
	}
	return s
}

func fjStatus(s string) string {
	switch s {
	case "pending":
		return "running"
	case "error", "failure":
		return "failure"
	case "warning":
		return "success"
	}
	return s
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// actionsRuns lists Actions runs and their jobs the GitHub way (GitHub; Gitea 1.24+): of a branch,
// or of a commit given in full.
func (c *Client) actionsRuns(ctx context.Context, srv Server, r Repo, ref string) ([]Check, bool, error) {
	var runs struct {
		Total int `json:"total_count"`
		Runs  []struct {
			ID         int64     `json:"id"`
			Name       string    `json:"name"`
			Title      string    `json:"display_title"`
			Status     string    `json:"status"`
			Conclusion string    `json:"conclusion"`
			HTMLURL    string    `json:"html_url"`
			Branch     string    `json:"head_branch"`
			SHA        string    `json:"head_sha"`
			Started    time.Time `json:"run_started_at"`
			Updated    time.Time `json:"updated_at"`
		} `json:"workflow_runs"`
	}
	// GitHub pages with per_page, Gitea with limit (at most 50 by default).
	size, per := 100, "per_page"
	if srv.Kind == Forgejo {
		size, per = 50, "limit"
	}
	q := "?" + per + "=10"
	switch {
	case fullSHA.MatchString(ref):
		q += "&head_sha=" + ref
	case ref != "":
		q += "&branch=" + url.QueryEscape(ref)
	}
	if _, err := c.get(ctx, srv, r.Host, "/repos/"+r.Path+"/actions/runs"+q, &runs, listMax); err != nil {
		return nil, false, err
	}
	var out []Check
	for _, run := range runs.Runs {
		name := run.Name
		if name == "" {
			name = run.Title
		}
		ch := Check{ID: strconv.FormatInt(run.ID, 10), Name: name, Status: ghStatus(run.Status, run.Conclusion), URL: run.HTMLURL,
			Ref: run.Branch, SHA: run.SHA, Started: run.Started, LogsByJobs: true}
		if run.Status == "completed" {
			ch.Finished = run.Updated
		}
		var jobs struct {
			Jobs []struct {
				ID         int64      `json:"id"`
				Name       string     `json:"name"`
				Status     string     `json:"status"`
				Conclusion string     `json:"conclusion"`
				HTMLURL    string     `json:"html_url"`
				Started    time.Time  `json:"started_at"`
				Completed  *time.Time `json:"completed_at"`
			} `json:"jobs"`
		}
		more, err := c.pages(ctx, srv, r.Host, "/repos/"+r.Path+"/actions/runs/"+ch.ID+"/jobs", url.Values{per: {strconv.Itoa(size)}}, size, func(get func(any) error) (int, error) {
			jobs.Jobs = nil
			if err := get(&jobs); err != nil {
				return 0, err
			}
			for _, j := range jobs.Jobs {
				job := Job{ID: strconv.FormatInt(j.ID, 10), Name: j.Name, Status: ghStatus(j.Status, j.Conclusion), URL: j.HTMLURL, Started: j.Started}
				if j.Completed != nil {
					job.Finished = *j.Completed
				}
				ch.Jobs = append(ch.Jobs, job)
			}
			return len(jobs.Jobs), nil
		})
		ch.JobsError = jobsError(more, err)
		out = append(out, ch)
	}
	return out, runs.Total > len(runs.Runs), nil
}

// shown is a value a server gave, fit to show: the host's token masked, cut to n.
func (c *Client) shown(host, v string, n int) string {
	if tok := c.token(host); tok != "" {
		v = strings.ReplaceAll(v, tok, "•••")
	}
	if r := []rune(v); len(r) > n {
		v = string(r[:n]) + "…"
	}
	return v
}

// plainURL is a link without what may be a credential in it: no user, query or fragment.
func plainURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "the job's page"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// alreadyOpen is what an open refused as a duplicate (GitHub 422, GitLab and Forgejo 409: one is
// open from this head) gives: the pull request open from that head (its branch in the same source
// repository: owner:branch names a fork's) into that base, as a repeat of the same open (a retry
// after a lost answer) expects. Any other refusal is the error it was; a lookup that fails is its
// own error (a 503, a broken connection: worth a retry), not the duplicate's.
func (c *Client) alreadyOpen(ctx context.Context, srv Server, r Repo, p PullRequest, err error) (Pull, error) {
	fe, ok := err.(*Error)
	if !ok || (fe.Status != 409 && fe.Status != 422) {
		return Pull{}, err
	}
	owner, _, _ := strings.Cut(r.Path, "/")
	src, ref := owner, p.Head
	if o, b, ok := strings.Cut(p.Head, ":"); ok && srv.Kind != GitLab {
		src, ref = o, b
		if o, _, ok := strings.Cut(src, "/"); ok { // owner/repo:branch
			src = o
		}
	}
	var filter url.Values
	switch srv.Kind {
	case GitHub:
		filter = url.Values{"head": {src + ":" + ref}, "base": {p.Base}}
	case GitLab:
		filter = url.Values{"source_branch": {ref}, "target_branch": {p.Base}}
	}
	list, lerr := c.listPulls(ctx, srv, r, "open", filter)
	if lerr != nil {
		return Pull{}, lerr
	}
	for _, x := range list.Items {
		from, _, _ := strings.Cut(x.HeadRepo, "/")
		if srv.Kind == GitLab {
			from = map[bool]string{true: src}[x.HeadRepo == r.Path] // a fork's MR is not this one
		}
		if x.Head == ref && x.Base == p.Base && strings.EqualFold(from, src) {
			x.Existing = true
			return x, nil
		}
	}
	return Pull{}, err
}

// DefaultBranch is the branch a repository's pull requests go into unless told otherwise.
func (c *Client) DefaultBranch(ctx context.Context, srv Server, r Repo) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	path := "/repos/" + r.Path
	if srv.Kind == GitLab {
		path = "/projects/" + glProject(r.Path)
	}
	if _, err := c.get(ctx, srv, r.Host, path, &repo, listMax); err != nil {
		return "", err
	}
	if repo.DefaultBranch == "" {
		return "", fmt.Errorf("%s: the forge names no default branch: --base names it", r.Web)
	}
	return repo.DefaultBranch, nil
}
