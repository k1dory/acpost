package forge

import (
	"context"
	"fmt"
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
	if state == "" {
		state = "open"
	}
	var out []Pull
	switch srv.Kind {
	case GitHub, Forgejo:
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
				Ref string `json:"ref"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
			Updated  time.Time  `json:"updated_at"`
			MergedAt *time.Time `json:"merged_at"`
			Merged   bool       `json:"merged"`
		}
		q := "?state=" + url.QueryEscape(state) + "&per_page=" + strconv.Itoa(pageLen)
		if srv.Kind == Forgejo {
			q = "?state=" + url.QueryEscape(state) + "&limit=" + strconv.Itoa(pageLen)
		}
		if _, err := c.get(ctx, srv, r.Host, "/repos/"+r.Path+"/pulls"+q, &list, listMax); err != nil {
			return nil, err
		}
		for _, p := range list {
			st := p.State
			if p.Merged || p.MergedAt != nil {
				st = "merged"
			}
			out = append(out, Pull{Number: p.Number, Title: p.Title, State: st, Draft: p.Draft, Author: p.User.Login,
				Head: p.Head.Ref, Base: p.Base.Ref, URL: p.HTMLURL, Updated: p.Updated})
		}
	case GitLab:
		gl := map[string]string{"open": "opened", "closed": "closed", "merged": "merged", "all": "all"}[state]
		if gl == "" {
			gl = "opened"
		}
		var list []struct {
			IID          int    `json:"iid"`
			Title        string `json:"title"`
			State        string `json:"state"`
			Draft        bool   `json:"draft"`
			WebURL       string `json:"web_url"`
			SourceBranch string `json:"source_branch"`
			TargetBranch string `json:"target_branch"`
			Author       struct {
				Username string `json:"username"`
			} `json:"author"`
			Updated time.Time `json:"updated_at"`
		}
		if _, err := c.get(ctx, srv, r.Host, "/projects/"+glProject(r.Path)+"/merge_requests?state="+gl+"&per_page="+strconv.Itoa(pageLen), &list, listMax); err != nil {
			return nil, err
		}
		for _, m := range list {
			st := map[string]string{"opened": "open"}[m.State]
			if st == "" {
				st = m.State
			}
			out = append(out, Pull{Number: m.IID, Title: m.Title, State: st, Draft: m.Draft, Author: m.Author.Username,
				Head: m.SourceBranch, Base: m.TargetBranch, URL: m.WebURL, Updated: m.Updated})
		}
	default:
		return nil, fmt.Errorf("forge kind %q", srv.Kind)
	}
	return out, nil
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
			return Pull{}, err
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
			return Pull{}, err
		}
		return Pull{Number: got.IID, Title: title, State: "open", Draft: p.Draft, Head: p.Head, Base: p.Base, URL: got.WebURL}, nil
	}
	return Pull{}, fmt.Errorf("forge kind %q", srv.Kind)
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
	var out []Check
	switch srv.Kind {
	case GitHub:
		runs, err := c.actionsRuns(ctx, srv, r, ref)
		if err != nil {
			return nil, err
		}
		out = runs
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
		if _, err := c.get(ctx, srv, r.Host, proj+"/pipelines"+q, &pipes, listMax); err != nil {
			return nil, err
		}
		for _, p := range pipes {
			ch := Check{ID: strconv.FormatInt(p.ID, 10), Name: "pipeline #" + strconv.FormatInt(p.ID, 10), Status: glStatus(p.Status),
				URL: p.WebURL, Ref: p.Ref, SHA: p.SHA, Started: p.Created, LogsByJobs: true}
			var jobs []struct {
				ID       int64      `json:"id"`
				Name     string     `json:"name"`
				Stage    string     `json:"stage"`
				Status   string     `json:"status"`
				WebURL   string     `json:"web_url"`
				Started  *time.Time `json:"started_at"`
				Finished *time.Time `json:"finished_at"`
			}
			if _, err := c.get(ctx, srv, r.Host, proj+"/pipelines/"+ch.ID+"/jobs?per_page=100", &jobs, listMax); err == nil {
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
			}
			out = append(out, ch)
		}
	case Forgejo:
		// Gitea 1.24+ has GitHub's Actions API (runs, jobs, a job's log); Forgejo and older Gitea
		// do not: then commit statuses, what Actions (and any CI) report on a commit.
		if runs, err := c.actionsRuns(ctx, srv, r, ref); err == nil {
			return runs, nil
		} else if fe, ok := err.(*Error); !ok || fe.Status != 404 {
			return nil, err
		}
		if ref == "" {
			return nil, fmt.Errorf("checks on a Forgejo/Gitea forge need a branch or commit")
		}
		var st []struct {
			ID        int64     `json:"id"`
			Context   string    `json:"context"`
			Status    string    `json:"status"`
			TargetURL string    `json:"target_url"`
			Created   time.Time `json:"created_at"`
			Updated   time.Time `json:"updated_at"`
		}
		if _, err := c.get(ctx, srv, r.Host, "/repos/"+r.Path+"/commits/"+url.PathEscape(ref)+"/statuses?limit=100", &st, listMax); err != nil {
			return nil, err
		}
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
		return nil, fmt.Errorf("forge kind %q", srv.Kind)
	}
	return out, nil
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
		noLogs := &Error{Status: 404, Message: fmt.Sprintf("%s (version %s) gives no job logs over its API: open %s", r.Host, srv.Version, target)}
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
func (c *Client) actionsRuns(ctx context.Context, srv Server, r Repo, ref string) ([]Check, error) {
	var runs struct {
		Runs []struct {
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
		return nil, err
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
		for page := 1; page <= 10; page++ { // all its jobs, a page at a time
			jobs.Jobs = nil
			path := "/repos/" + r.Path + "/actions/runs/" + ch.ID + "/jobs?" + per + "=" + strconv.Itoa(size) + "&page=" + strconv.Itoa(page)
			if _, err := c.get(ctx, srv, r.Host, path, &jobs, listMax); err != nil {
				break
			}
			for _, j := range jobs.Jobs {
				job := Job{ID: strconv.FormatInt(j.ID, 10), Name: j.Name, Status: ghStatus(j.Status, j.Conclusion), URL: j.HTMLURL, Started: j.Started}
				if j.Completed != nil {
					job.Finished = *j.Completed
				}
				ch.Jobs = append(ch.Jobs, job)
			}
			if len(jobs.Jobs) < size {
				break
			}
		}
		out = append(out, ch)
	}
	return out, nil
}
