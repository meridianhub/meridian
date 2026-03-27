package main

import (
	"context"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"

	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcache "github.com/meridianhub/meridian/services/reference-data/cache"
	refhandler "github.com/meridianhub/meridian/services/reference-data/handler"
	refregistry "github.com/meridianhub/meridian/services/reference-data/registry"
)

// registryAccountTypeLoader adapts accounttype.PostgresRegistry to cache.AccountTypeLoader.
type registryAccountTypeLoader struct {
	registry *accounttype.PostgresRegistry
}

func (l *registryAccountTypeLoader) LoadAccountType(ctx context.Context, code string) (*accounttype.Definition, error) {
	return l.registry.GetActiveDefinition(ctx, code)
}

func (l *registryAccountTypeLoader) ListActiveAccountTypes(ctx context.Context) ([]*accounttype.Definition, error) {
	return l.registry.ListActive(ctx)
}

// inProcessRefDataClient adapts the in-process reference-data gRPC handler
// to the ReferenceDataClient interface used by internal-account service.
type inProcessRefDataClient struct {
	svc *refhandler.Service
}

func (c *inProcessRefDataClient) RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
	return c.svc.RetrieveInstrument(ctx, req)
}

func (c *inProcessRefDataClient) Close() error { return nil }

// inProcessInstrumentGetter adapts the in-process instrument registry to
// the InstrumentGetter interface used by the current-account service for
// dimension resolution during account creation.
type inProcessInstrumentGetter struct {
	registry refregistry.InstrumentRegistry
}

func (g *inProcessInstrumentGetter) GetInstrument(ctx context.Context, code string, version int) (*refcache.CachedInstrument, error) {
	var def *refregistry.InstrumentDefinition
	var err error
	if version > 0 {
		def, err = g.registry.GetDefinition(ctx, code, version)
	} else {
		def, err = g.registry.GetActiveDefinition(ctx, code)
	}
	if err != nil {
		return nil, err
	}
	return &refcache.CachedInstrument{Definition: def}, nil
}

// loopbackApplyManifestAdapter adapts gRPC ApplyManifestServiceClient to manifest.Applier.
type loopbackApplyManifestAdapter struct {
	c controlplanev1.ApplyManifestServiceClient
}

func (a loopbackApplyManifestAdapter) ApplyManifest(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
	return a.c.ApplyManifest(ctx, req)
}
