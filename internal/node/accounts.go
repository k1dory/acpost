package node

import (
	"context"
	"regexp"

	"github.com/k1dory/acpost/internal/client"
)

// The node user's forge accounts (the app's Settings; acpost forge login on the command line).

var forgeHost = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}(:[0-9]{1,5})?$`)

// ForgeAccounts lists the hosts logged in to: their kind and version, and whose the token is.
func (n *Node) ForgeAccounts(ctx context.Context, p Principal) ([]client.ForgeAccount, error) {
	if err := userOnly(p, "forge accounts"); err != nil {
		return nil, err
	}
	c, st := n.forgeClient()
	out := []client.ForgeAccount{}
	for _, h := range st.Hosts() {
		a := client.ForgeAccount{Host: h, Kind: st.Kind(h), Authed: st.Token(h) != ""}
		srv, err := c.Probe(ctx, h)
		if err != nil {
			a.Error = err.Error()
		} else {
			a.Kind, a.Version = srv.Kind, srv.Version
			if a.Authed {
				if login, err := c.Whoami(ctx, srv, h); err == nil {
					a.Login = login
				} else {
					a.Error = forgeErr(err).Error()
				}
			}
		}
		out = append(out, a)
	}
	return out, nil
}

// ForgeLogin keeps a host's token once the forge says whose it is (a token it refuses is not kept).
func (n *Node) ForgeLogin(ctx context.Context, p Principal, host, kind, token string) (client.ForgeAccount, error) {
	if err := userOnly(p, "forge accounts"); err != nil {
		return client.ForgeAccount{}, err
	}
	if !forgeHost.MatchString(host) {
		return client.ForgeAccount{}, errf("invalid", "%q: a host (github.com, gitlab.example.com:8443), no scheme or path", host)
	}
	switch kind {
	case "", "github", "gitlab", "forgejo":
	default:
		return client.ForgeAccount{}, errf("invalid", "kind %q: github, gitlab or forgejo", kind)
	}
	c, st := n.forgeClient()
	if err := st.Login(host, kind, token); err != nil {
		return client.ForgeAccount{}, errf("invalid", "%v", err)
	}
	n.forges.mu.Lock()
	delete(n.forges.probed, host)
	n.forges.mu.Unlock()
	srv, err := c.Probe(ctx, host)
	if err != nil {
		_ = st.Logout(host)
		return client.ForgeAccount{}, errh("invalid", "name its kind (github, gitlab, forgejo)", "%v", err)
	}
	login, err := c.Whoami(ctx, srv, host)
	if err != nil {
		_ = st.Logout(host)
		return client.ForgeAccount{}, forgeErr(err)
	}
	return client.ForgeAccount{Host: host, Kind: srv.Kind, Version: srv.Version, Authed: true, Login: login}, nil
}

// ForgeLogout forgets a host's token.
func (n *Node) ForgeLogout(ctx context.Context, p Principal, host string) error {
	if err := userOnly(p, "forge accounts"); err != nil {
		return err
	}
	_, st := n.forgeClient()
	if err := st.Logout(host); err != nil {
		return errf("unavailable", "%v", err)
	}
	n.forges.mu.Lock()
	delete(n.forges.probed, host)
	n.forges.mu.Unlock()
	return nil
}
