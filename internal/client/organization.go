package client

import (
	"context"
	"fmt"
	"sync"
)

// One Automox organization has two identifiers, and different endpoint families
// demand different ones:
//
//	?o={id}                       integer  policies, servergroups, servers, events,
//	                                       policystats, reports, data-extracts
//	/orgs/{orgID}/…               integer  org packages, remediations/action-sets
//	/…/org/{orgUUID}/…            UUID     policy-windows, device-details
//
// Practitioners configure only the integer, because that is what the Automox
// console surfaces. The UUID is resolved here so no configuration has to carry
// both and risk them drifting apart.

// Organization is the subset of GET /orgs the provider needs. The endpoint
// returns considerably more, including an access_key that is a credential and is
// deliberately not modelled here.
type Organization struct {
	ID       int64  `json:"id"`
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	ParentID *int64 `json:"parent_id"`
}

type orgCache struct {
	mu       sync.RWMutex
	loaded   bool
	byID     map[int64]Organization
	loadErr  error
	attempts int
}

func newOrgCache() *orgCache {
	return &orgCache{byID: make(map[int64]Organization)}
}

// OrganizationUUID returns the UUID for an integer organization id.
//
// The listing is fetched once per provider instance. GET /orgs is not scoped to
// an organization and is readable by any valid key, so this works even for the
// organization-scoped credentials that cannot reach /accounts/*.
func (c *Client) OrganizationUUID(ctx context.Context, orgID int64) (string, error) {
	if orgID == 0 {
		return "", fmt.Errorf("automox: cannot resolve an organization UUID without an organization id")
	}

	orgs, err := c.organizations(ctx)
	if err != nil {
		return "", err
	}

	org, ok := orgs[orgID]
	if !ok {
		// Listing succeeded but the id is absent. That is a configuration error,
		// not a transient one, so name what was visible instead of failing bare.
		return "", fmt.Errorf(
			"automox: organization %d was not found among the %d organization(s) this credential can see%s",
			orgID, len(orgs), visibleOrgs(orgs))
	}

	if org.UUID == "" {
		return "", fmt.Errorf(
			"automox: organization %d (%q) returned no uuid, which endpoints scoped by "+
				"organization UUID require", orgID, org.Name)
	}

	return org.UUID, nil
}

// DefaultOrganizationUUID resolves the UUID of the configured organization.
func (c *Client) DefaultOrganizationUUID(ctx context.Context) (string, error) {
	return c.OrganizationUUID(ctx, c.orgID)
}

// Organizations returns every organization the credential can see.
func (c *Client) Organizations(ctx context.Context) ([]Organization, error) {
	byID, err := c.organizations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Organization, 0, len(byID))
	for _, o := range byID {
		out = append(out, o)
	}
	return out, nil
}

func (c *Client) organizations(ctx context.Context) (map[int64]Organization, error) {
	c.orgs.mu.RLock()
	if c.orgs.loaded {
		defer c.orgs.mu.RUnlock()
		return c.orgs.byID, c.orgs.loadErr
	}
	c.orgs.mu.RUnlock()

	c.orgs.mu.Lock()
	defer c.orgs.mu.Unlock()

	// Another goroutine may have loaded while we waited for the write lock.
	if c.orgs.loaded {
		return c.orgs.byID, c.orgs.loadErr
	}

	var orgs []Organization
	err := c.Do(ctx, Request{
		Method:   "GET",
		Path:     "/orgs",
		OrgScope: OrgScopeNone,
	}, &orgs)

	c.orgs.attempts++

	if err != nil {
		// Do not cache a failure. A transient outage or a credential being
		// repaired mid-run should not poison every later lookup; caching the
		// error would turn one blip into a failed apply.
		return nil, fmt.Errorf("automox: listing organizations to resolve identifiers: %w", err)
	}

	byID := make(map[int64]Organization, len(orgs))
	for _, o := range orgs {
		byID[o.ID] = o
	}

	c.orgs.byID = byID
	c.orgs.loaded = true
	return byID, nil
}

func visibleOrgs(orgs map[int64]Organization) string {
	if len(orgs) == 0 {
		return ""
	}
	s := ": "
	first := true
	for id, o := range orgs {
		if !first {
			s += ", "
		}
		s += fmt.Sprintf("%d (%q)", id, o.Name)
		first = false
	}
	return s
}
