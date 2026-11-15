package siteenv

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// RenameEnvironment renames an environment between dev and prod (#2630). The
// new name passes the environment-name and address rules, is not dev, prod or
// the old alias, and is not in use. Every reference to the old name moves in
// one transaction; the environment answers at <site>--<new> at once, and the
// old address answers nothing. It is anyone's who may change the
// environment's settings.
func (o Ops) RenameEnvironment(ctx context.Context, actor sitesvc.Actor, ref, from, to string) (*site.Site, error) {
	st, env, err := o.between(ctx, actor, ref, from, "renamed")
	if err != nil {
		return nil, err
	}
	to = strings.TrimSpace(to)
	switch to {
	case site.EnvDev, site.EnvProd, site.EnvAlias:
		return nil, refused("%q is a name the platform keeps: every site has dev and prod, and %q is the old name of dev. Choose another name.", to, site.EnvAlias)
	case env.Name:
		return nil, refused("%s is already named %s", env.Name, to)
	}
	if st.Environment(to) != nil {
		return nil, refused("the site already has an environment named %q: choose another name", to)
	}
	if err := o.CheckNewEnvironment(ctx, st, to); err != nil {
		return nil, err //nolint:wrapcheck // the service's refusal
	}
	if err := o.Store.RenameEnvironment(ctx, st.ID, env.Name, to, actor.Email); err != nil {
		return nil, inUse(err)
	}
	o.invalidate(st.Name)
	return o.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
}

// RemoveEnvironment removes an environment between dev and prod (#2630). It
// is refused while the environment has a domain, holds the main address, is a
// part of an application, or has a job running, each naming what to do first.
// Removing one that requires approval takes an approval gate out of the
// promotion path, so it is an administrator's.
func (o Ops) RemoveEnvironment(ctx context.Context, actor sitesvc.Actor, ref, name string) (*site.Site, error) {
	st, env, err := o.between(ctx, actor, ref, name, "removed")
	if err != nil {
		return nil, err
	}
	if env.RequiresApproval && !actor.Admin {
		return nil, fmt.Errorf("an administrator removes an environment that requires approval, since removing it takes an approval out of the promotion path: %w",
			site.ErrNotAllowed)
	}
	if err := o.Store.RemoveEnvironment(ctx, st.ID, env.Name); err != nil {
		return nil, inUse(err)
	}
	o.invalidate(st.Name)
	return o.Store.Get(ctx, st.ID) //nolint:wrapcheck // read by identity
}

// between is a site and one of its environments between dev and prod, for
// someone who may change it.
func (o Ops) between(ctx context.Context, actor sitesvc.Actor, ref, name, verb string) (*site.Site, *site.Environment, error) {
	name, _ = site.CanonicalEnvironment(strings.TrimSpace(name))
	if name == site.EnvDev || name == site.EnvProd {
		return nil, nil, refused("every site has dev and prod, so %s cannot be %s: only the environments between them can be", name, verb)
	}
	return o.siteFor(ctx, actor, ref, name)
}

// inUse is the refusal a store's ErrEnvironmentInUse stands for, in its own
// words; any other error as it is.
func inUse(err error) error {
	if held, ok := errors.AsType[*site.InUseError](err); ok {
		return &sitesvc.RefusedError{Msg: held.Msg, Err: site.ErrEnvironmentInUse}
	}
	return err
}

// nameRefusal is why ValidateName refuses name, "" for none.
func nameRefusal(name string) string {
	if err := site.ValidateName(name); err != nil {
		return err.Error()
	}
	return ""
}

// sentence is a refusal as the dialog a person typed into shows it: the
// service's error text starts lowercase and ends bare, as Go errors do, and a
// person reads a sentence.
func sentence(msg string) string {
	if msg == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(msg)
	msg = string(unicode.ToUpper(r)) + msg[size:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// NameCheck is what the text a person typed for a new site's name becomes
// (#2636): the name, the addresses its two environments get, how long an
// environment name it leaves room for, and the reason it is refused, if it is.
// The rule lives on the server alone; the portal's New site dialog asks it.
type NameCheck struct {
	Input string `json:"input" example:"My Website"`
	Name  string `json:"name" example:"my-website"`
	// Addresses is where dev and prod would answer on the pages domain, ""
	// with no pages domain.
	Addresses map[string]string `json:"addresses"`
	// EnvironmentNameRoom is the longest environment name the site can add.
	EnvironmentNameRoom int `json:"environment_name_room" example:"32"`
	// Refusal is why the name cannot be created, absent when it can.
	Refusal string `json:"refusal,omitempty"`
}

// CheckName answers what input becomes as a site name, and whether Create
// would take it, without creating anything.
func (o Ops) CheckName(ctx context.Context, input string) (NameCheck, error) {
	name := site.NameFrom(input)
	out := NameCheck{Input: input, Name: name, Addresses: map[string]string{}}
	if name == "" {
		out.Refusal = "Type a name with at least one letter or digit."
		return out, nil
	}
	st := &site.Site{Name: name, Environments: []site.Environment{{Name: site.EnvDev}, {Name: site.EnvProd}}, MainEnvironment: site.EnvProd}
	at := o.Addressing()
	for _, env := range []string{site.EnvDev, site.EnvProd} {
		out.Addresses[env] = at.PagesAddress(st, env)
	}
	out.EnvironmentNameRoom = site.EnvironmentNameRoom(name)
	refusal, err := o.refusalOf(ctx, name)
	out.Refusal = sentence(refusal)
	return out, err
}

// refusalOf is why Create would refuse name, "" for none: its form and labels,
// a name a site or an application holds.
func (o Ops) refusalOf(ctx context.Context, name string) (string, error) {
	if msg := nameRefusal(name); msg != "" {
		return msg, nil
	}
	if o.Apps != nil {
		for _, label := range []string{name, site.EnvLabel(name, site.EnvDev), site.EnvLabel(name, site.EnvProd)} {
			if _, err := o.Apps.GetByName(ctx, label); err == nil {
				return fmt.Sprintf("%q is the name of an application, and a site address and an application cannot share a name", label), nil
			}
		}
	}
	switch _, err := o.Store.GetByName(ctx, name); {
	case err == nil:
		return fmt.Sprintf("A site named %q exists. Choose another name.", name), nil
	case errors.Is(err, site.ErrNotFound):
		return "", nil
	default:
		return "", fmt.Errorf("reading sites: %w", err)
	}
}
