// SPDX-License-Identifier: Apache-2.0

package plugintest

import (
	"context"
	"slices"
	"strings"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/plugin"
)

// Package is a fake package: what it offers, the sources it provides and
// whether it starts out installed.
type Package struct {
	Package *sourcev1.Package
	Sources []*Source

	installed string
}

// Offer is where a fake package can be installed from.
type Offer struct{ OriginID, OriginName, Version string }

// NewPackage describes a package that provides sources, offered by origins.
// Its version is the newest offered.
func NewPackage(id, name, version string, sources []*Source, offers ...Offer) *Package {
	p := &Package{Sources: sources, Package: &sourcev1.Package{
		Id: id, Name: name, Version: version, Supported: true,
		ContentRating: sourcev1.ContentRating_CONTENT_RATING_SAFE,
	}}
	for _, o := range offers {
		p.Package.Offers = append(p.Package.Offers, &sourcev1.PackageOffer{OriginId: o.OriginID, OriginName: o.OriginName, Version: o.Version, Supported: true})
	}
	for _, s := range sources {
		if !slices.Contains(p.Package.Langs, s.Source.GetLang()) {
			p.Package.Langs = append(p.Package.Langs, s.Source.GetLang())
		}
	}
	return p
}

// Installed makes the package start out installed at version.
func (p *Package) Installed(version string) *Package {
	p.installed = version
	return p
}

// packageState is what changes while serving.
type packageState struct {
	installed string
	langs     []string
}

func (f *Fake) state(p *Package) *packageState {
	if f.pkgState == nil {
		f.pkgState = map[string]*packageState{}
	}
	st, ok := f.pkgState[p.Package.GetId()]
	if !ok {
		st = &packageState{installed: p.installed}
		f.pkgState[p.Package.GetId()] = st
	}
	return st
}

func (f *Fake) capabilities() []sourcev1.PluginCapability {
	caps := []sourcev1.PluginCapability{sourcev1.PluginCapability_PLUGIN_CAPABILITY_NETWORK_PROFILES}
	if f.Packages != nil {
		caps = append(caps, sourcev1.PluginCapability_PLUGIN_CAPABILITY_PACKAGES, sourcev1.PluginCapability_PLUGIN_CAPABILITY_DYNAMIC_SOURCES)
	}
	return caps
}

func (f *Fake) packageOf(sourceID string) *Package {
	for _, p := range f.Packages {
		for _, s := range p.Sources {
			if s.Source.GetId() == sourceID {
				return p
			}
		}
	}
	return nil
}

// langEnabled says whether a source language is enabled at both levels.
func langEnabled(lang string, plugin, pkg []string) bool {
	return (len(plugin) == 0 || slices.Contains(plugin, lang)) && (len(pkg) == 0 || slices.Contains(pkg, lang))
}

// listed says whether a source is served: always without packages; else when
// its package is installed and its language enabled.
func (f *Fake) listed(s *Source) bool {
	p := f.packageOf(s.Source.GetId())
	if p == nil {
		return f.Packages == nil || len(f.Langs) == 0 || slices.Contains(f.Langs, s.Source.GetLang())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.state(p)
	return st.installed != "" && langEnabled(s.Source.GetLang(), f.Langs, st.langs)
}

func (f *Fake) findPackage(id string) (*Package, error) {
	if f.Packages == nil {
		return nil, plugin.ErrUnimplemented("packages")
	}
	for _, p := range f.Packages {
		if p.Package.GetId() == id {
			return p, nil
		}
	}
	return nil, plugin.ErrNotFound("package %q not found", id)
}

// view is the package as the API shows it now. Call with f.mu held.
func (f *Fake) view(p *Package) *sourcev1.Package {
	st := f.state(p)
	out := &sourcev1.Package{
		Id: p.Package.GetId(), Name: p.Package.GetName(), Langs: p.Package.GetLangs(), Version: p.Package.GetVersion(),
		Installed: st.installed != "", InstalledVersion: st.installed, ContentRating: p.Package.GetContentRating(),
		Supported: p.Package.GetSupported(), Offers: p.Package.GetOffers(),
		Settings: &sourcev1.PackageSettings{EnabledLangs: slices.Clone(st.langs)},
	}
	out.UpdateAvailable = out.Installed && st.installed != p.Package.GetVersion()
	for _, s := range p.Sources {
		out.Sources = append(out.Sources, &sourcev1.PackageSource{
			Id: s.Source.GetId(), Name: s.Source.GetName(), Lang: s.Source.GetLang(),
			Available: langEnabled(s.Source.GetLang(), f.Langs, st.langs),
		})
	}
	return out
}

func (f *Fake) ListPackages(_ context.Context, r *sourcev1.ListPackagesRequest) (*sourcev1.ListPackagesResponse, error) {
	f.count("ListPackages")
	if f.Packages == nil {
		return nil, plugin.ErrUnimplemented("ListPackages")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	res := &sourcev1.ListPackagesResponse{}
	q := strings.ToLower(r.GetQuery())
	for _, p := range f.Packages {
		v := f.view(p)
		switch {
		case q != "" && !strings.Contains(strings.ToLower(v.GetName()), q):
		case r.GetLang() != "" && !slices.Contains(v.GetLangs(), r.GetLang()):
		case r.GetInstalledOnly() && !v.GetInstalled():
		case r.GetUpdatesOnly() && !v.GetUpdateAvailable():
		case r.GetOriginId() != "" && !slices.ContainsFunc(v.GetOffers(), func(o *sourcev1.PackageOffer) bool { return o.GetOriginId() == r.GetOriginId() }):
		default:
			res.Packages = append(res.Packages, v)
		}
	}
	return res, nil
}

func (f *Fake) GetPackage(_ context.Context, r *sourcev1.GetPackageRequest) (*sourcev1.GetPackageResponse, error) {
	p, err := f.findPackage(r.GetPackageId())
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &sourcev1.GetPackageResponse{Package: f.view(p)}, nil
}

// InstallPackage installs the offered version; the same version again is OK.
func (f *Fake) InstallPackage(_ context.Context, r *sourcev1.InstallPackageRequest) (*sourcev1.InstallPackageResponse, error) {
	f.count("InstallPackage")
	p, err := f.findPackage(r.GetPackageId())
	if err != nil {
		return nil, err
	}
	version := p.Package.GetVersion()
	if r.GetOriginId() != "" {
		i := slices.IndexFunc(p.Package.GetOffers(), func(o *sourcev1.PackageOffer) bool { return o.GetOriginId() == r.GetOriginId() })
		if i < 0 {
			return nil, plugin.ErrInvalidArgument("origin %q doesn't offer %q", r.GetOriginId(), r.GetPackageId())
		}
		version = p.Package.GetOffers()[i].GetVersion()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state(p).installed = version
	return &sourcev1.InstallPackageResponse{Package: f.view(p)}, nil
}

func (f *Fake) UpdatePackage(_ context.Context, r *sourcev1.UpdatePackageRequest) (*sourcev1.UpdatePackageResponse, error) {
	f.count("UpdatePackage")
	p, err := f.findPackage(r.GetPackageId())
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.state(p)
	if st.installed == "" {
		return nil, plugin.Errorf(plugin.CodeFailedPrecondition, "package %q isn't installed", r.GetPackageId())
	}
	st.installed = p.Package.GetVersion()
	return &sourcev1.UpdatePackageResponse{Package: f.view(p)}, nil
}

func (f *Fake) UninstallPackage(_ context.Context, r *sourcev1.UninstallPackageRequest) (*sourcev1.UninstallPackageResponse, error) {
	f.count("UninstallPackage")
	p, err := f.findPackage(r.GetPackageId())
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state(p).installed = ""
	return &sourcev1.UninstallPackageResponse{}, nil
}

func (f *Fake) SetPackageSettings(_ context.Context, r *sourcev1.SetPackageSettingsRequest) (*sourcev1.SetPackageSettingsResponse, error) {
	p, err := f.findPackage(r.GetPackageId())
	if err != nil {
		return nil, err
	}
	for _, l := range r.GetSettings().GetEnabledLangs() {
		if !slices.Contains(p.Package.GetLangs(), l) {
			return nil, plugin.ErrInvalidArgument("package %q has no %q sources", r.GetPackageId(), l)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state(p).langs = slices.Clone(r.GetSettings().GetEnabledLangs())
	return &sourcev1.SetPackageSettingsResponse{Package: f.view(p)}, nil
}
