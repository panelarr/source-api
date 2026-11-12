// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"context"
	"fmt"
	"slices"
	"time"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/client"
	"github.com/panelarr/source-api/sdk/plugin"
)

func checkPackages(ctx context.Context, rep *Report, c client.Client, info *sourcev1.PluginInfo, packageID string) {
	if !slices.Contains(info.GetCapabilities(), sourcev1.PluginCapability_PLUGIN_CAPABILITY_PACKAGES) {
		if _, err := c.ListPackages(ctx, &sourcev1.ListPackagesRequest{}); plugin.ErrorCode(err) != plugin.CodeUnimplemented {
			rep.warn("ListPackages: a plugin without the PACKAGES capability should answer UNIMPLEMENTED, got %v", err)
		}
		if packageID != "" {
			rep.problem("a package to install was given, but the plugin declares no PACKAGES capability")
		}
		return
	}
	res, err := c.ListPackages(ctx, &sourcev1.ListPackagesRequest{})
	if err != nil {
		rep.problem("ListPackages: %v", err)
		return
	}
	rep.Packages = len(res.GetPackages())
	byID := map[string]*sourcev1.Package{}
	for i, p := range res.GetPackages() {
		checkPackage(rep, fmt.Sprintf("ListPackages: package %d", i), p)
		if byID[p.GetId()] != nil {
			rep.problem("ListPackages: package id %q appears twice", p.GetId())
		}
		byID[p.GetId()] = p
	}
	checkSourcesMatchPackages(ctx, rep, c, byID)
	checkPackageFilters(ctx, rep, c, res.GetPackages())
	checkOrigins(ctx, rep, c, res.GetPackages())
	if _, err := c.GetPackage(ctx, &sourcev1.GetPackageRequest{PackageId: "conformance.no-such-package"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		rep.problem("GetPackage: an unknown package must be NOT_FOUND, got %v", err)
	}
	switch _, err := c.ReloadPackage(ctx, &sourcev1.ReloadPackageRequest{PackageId: "conformance.no-such-package"}); plugin.ErrorCode(err) {
	case plugin.CodeNotFound:
	case plugin.CodeUnimplemented:
		rep.warn("ReloadPackage: not implemented, so hosts can't reload a package")
	default:
		rep.problem("ReloadPackage: an unknown package must be NOT_FOUND, got %v", err)
	}
	if packageID != "" {
		p := byID[packageID]
		if p == nil {
			rep.problem("the package to install, %q, isn't listed", packageID)
			return
		}
		(&packageWalk{ctx: ctx, rep: rep, c: c, pkg: p}).run()
	}
}

func checkPackage(rep *Report, at string, p *sourcev1.Package) {
	if !plugin.PackageIDPattern.MatchString(p.GetId()) {
		rep.problem("%s: id %q must match %s", at, p.GetId(), plugin.PackageIDPattern)
	}
	if p.GetName() == "" {
		rep.problem("%s (%s): no name", at, p.GetId())
	}
	if p.GetInstalled() && p.GetInstalledVersion() == "" {
		rep.problem("%s (%s): installed without an installed version", at, p.GetId())
	}
	if p.GetDetails() != nil && !p.GetInstalled() {
		rep.problem("%s (%s): details are for installed packages", at, p.GetId())
	}
	if p.GetUpdateAvailable() && !p.GetInstalled() {
		rep.problem("%s (%s): an update is available but it isn't installed", at, p.GetId())
	}
	for _, o := range p.GetOffers() {
		if o.GetOriginId() == "" {
			rep.problem("%s (%s): an offer has no origin id", at, p.GetId())
		}
	}
	for _, s := range p.GetSources() {
		if s.GetId() == "" || s.GetLang() == "" {
			rep.problem("%s (%s): a source needs an id and a language", at, p.GetId())
		}
		checkCompatIDs(rep, fmt.Sprintf("%s (%s): source %s", at, p.GetId(), s.GetId()), s.GetCompatIds())
		if !slices.Contains(p.GetLangs(), s.GetLang()) {
			rep.problem("%s (%s): source %s's language %q isn't among the package's languages", at, p.GetId(), s.GetId(), s.GetLang())
		}
	}
	for _, l := range p.GetSettings().GetEnabledLangs() {
		if !slices.Contains(p.GetLangs(), l) {
			rep.problem("%s (%s): enabled language %q isn't among the package's languages", at, p.GetId(), l)
		}
	}
}

// checkOrigins: ListOrigins lists every origin the packages' offers name.
func checkOrigins(ctx context.Context, rep *Report, c client.Client, pkgs []*sourcev1.Package) {
	res, err := c.ListOrigins(ctx, &sourcev1.ListOriginsRequest{})
	if plugin.ErrorCode(err) == plugin.CodeUnimplemented {
		rep.warn("ListOrigins: not implemented, so hosts can't show where packages come from")
		return
	}
	if err != nil {
		rep.problem("ListOrigins: %v", err)
		return
	}
	ids := map[string]bool{}
	for i, o := range res.GetOrigins() {
		if !plugin.PackageIDPattern.MatchString(o.GetId()) {
			rep.problem("ListOrigins: origin %d: id %q must match %s", i, o.GetId(), plugin.PackageIDPattern)
		}
		if o.GetName() == "" {
			rep.problem("ListOrigins: origin %d (%s): no name", i, o.GetId())
		}
		if ids[o.GetId()] {
			rep.problem("ListOrigins: origin id %q appears twice", o.GetId())
		}
		ids[o.GetId()] = true
	}
	for _, p := range pkgs {
		for _, o := range p.GetOffers() {
			if !ids[o.GetOriginId()] {
				rep.problem("ListOrigins: origin %q, which offers package %q, isn't listed", o.GetOriginId(), p.GetId())
				ids[o.GetOriginId()] = true // once is enough
			}
		}
	}
	if _, err := c.RefreshOrigin(ctx, &sourcev1.RefreshOriginRequest{OriginId: "conformance.no-such-origin"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		rep.problem("RefreshOrigin: an unknown origin must be NOT_FOUND, got %v", err)
	}
}

// checkSourcesMatchPackages: ListSources lists exactly the available sources of
// installed packages.
func checkSourcesMatchPackages(ctx context.Context, rep *Report, c client.Client, byID map[string]*sourcev1.Package) {
	listed, err := c.ListSources(ctx, &sourcev1.ListSourcesRequest{})
	if err != nil {
		return // reported by checkSources
	}
	ids := map[string]*sourcev1.Source{}
	for _, s := range listed.GetSources() {
		ids[s.GetId()] = s
		if s.GetPackageId() == "" {
			continue
		}
		p := byID[s.GetPackageId()]
		switch {
		case p == nil:
			rep.problem("ListSources: source %s names package %q, which isn't listed", s.GetId(), s.GetPackageId())
		case !p.GetInstalled():
			rep.problem("ListSources: source %s belongs to package %q, which isn't installed", s.GetId(), s.GetPackageId())
		}
	}
	for _, p := range byID {
		if !p.GetInstalled() || p.GetError() != "" {
			continue
		}
		for _, ps := range p.GetSources() {
			s, ok := ids[ps.GetId()]
			switch {
			case ps.GetAvailable() && !ok:
				rep.problem("ListSources: source %s of installed package %q is available but not listed", ps.GetId(), p.GetId())
			case !ps.GetAvailable() && ok:
				rep.problem("ListSources: source %s of package %q is listed but its language isn't enabled", ps.GetId(), p.GetId())
			case ok && s.GetPackageId() != p.GetId():
				rep.problem("ListSources: source %s should name its package %q", ps.GetId(), p.GetId())
			}
			if ok && !sameSet(s.GetCompatIds(), ps.GetCompatIds()) {
				rep.warn("ListSources: source %s has compat ids %q, but package %q lists %q for it", ps.GetId(), s.GetCompatIds(), p.GetId(), ps.GetCompatIds())
			}
		}
	}
}

func checkPackageFilters(ctx context.Context, rep *Report, c client.Client, all []*sourcev1.Package) {
	installed, err := c.ListPackages(ctx, &sourcev1.ListPackagesRequest{InstalledOnly: true})
	if err != nil {
		rep.problem("ListPackages(installed_only): %v", err)
	} else if slices.ContainsFunc(installed.GetPackages(), func(p *sourcev1.Package) bool { return !p.GetInstalled() }) {
		rep.problem("ListPackages(installed_only): returned a package that isn't installed")
	}
	if len(all) == 0 || len(all[0].GetLangs()) == 0 {
		return
	}
	lang := all[0].GetLangs()[0]
	byLang, err := c.ListPackages(ctx, &sourcev1.ListPackagesRequest{Lang: lang})
	if err != nil {
		rep.problem("ListPackages(lang=%s): %v", lang, err)
	} else if slices.ContainsFunc(byLang.GetPackages(), func(p *sourcev1.Package) bool { return !slices.Contains(p.GetLangs(), lang) }) {
		rep.problem("ListPackages(lang=%s): returned a package without that language", lang)
	}
}

// packageWalk installs a package, narrows it to one language, restores it and
// removes it again if it wasn't installed before.
type packageWalk struct {
	ctx context.Context
	rep *Report
	c   client.Client
	pkg *sourcev1.Package
}

func (w *packageWalk) step(name string, f func() (string, error)) bool {
	started := time.Now()
	detail, err := f()
	st := Step{Name: name, OK: err == nil, Detail: detail, Duration: time.Since(started)}
	if err != nil {
		st.Detail, st.Reason = err.Error(), string(plugin.ErrorReason(err))
		checkError(w.rep, name+" (package "+w.pkg.GetId()+")", err)
	}
	w.rep.PackageSteps = append(w.rep.PackageSteps, st)
	return err == nil
}

func (w *packageWalk) listedOf() (map[string]string, error) {
	res, err := w.c.ListSources(w.ctx, &sourcev1.ListSourcesRequest{})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range res.GetSources() {
		if s.GetPackageId() == w.pkg.GetId() {
			out[s.GetId()] = s.GetLang()
		}
	}
	return out, nil
}

func (w *packageWalk) run() {
	id := w.pkg.GetId()
	wasInstalled := w.pkg.GetInstalled()
	original := w.pkg.GetSettings()
	var installed *sourcev1.Package
	if !w.step("InstallPackage", func() (string, error) {
		res, err := w.c.InstallPackage(w.ctx, &sourcev1.InstallPackageRequest{PackageId: id})
		if err != nil {
			return "", err
		}
		installed = res.GetPackage()
		if !installed.GetInstalled() {
			w.rep.problem("InstallPackage (%s): the package isn't installed afterwards", id)
		}
		return "version " + installed.GetInstalledVersion(), nil
	}) {
		return
	}
	w.step("InstallPackage again", func() (string, error) {
		res, err := w.c.InstallPackage(w.ctx, &sourcev1.InstallPackageRequest{PackageId: id})
		if err != nil {
			w.rep.problem("InstallPackage (%s): installing the installed version again must answer OK: %v", id, err)
			return "", err
		}
		return "version " + res.GetPackage().GetInstalledVersion(), nil
	})
	w.step("Sources listed", func() (string, error) {
		listed, err := w.listedOf()
		if err != nil {
			return "", err
		}
		for _, ps := range installed.GetSources() {
			if _, ok := listed[ps.GetId()]; ps.GetAvailable() && !ok {
				w.rep.problem("InstallPackage (%s): source %s isn't listed after the install", id, ps.GetId())
			}
		}
		return fmt.Sprintf("%d sources", len(listed)), nil
	})
	w.step("ReloadPackage", func() (string, error) {
		res, err := w.c.ReloadPackage(w.ctx, &sourcev1.ReloadPackageRequest{PackageId: id})
		if plugin.ErrorCode(err) == plugin.CodeUnimplemented {
			return "not implemented", nil
		}
		if err != nil {
			return "", err
		}
		if !res.GetPackage().GetInstalled() {
			w.rep.problem("ReloadPackage (%s): the package isn't installed afterwards", id)
		}
		return "version " + res.GetPackage().GetInstalledVersion(), nil
	})
	if langs := installed.GetLangs(); len(langs) > 1 {
		w.step("Narrow languages", func() (string, error) {
			_, err := w.c.SetPackageSettings(w.ctx, &sourcev1.SetPackageSettingsRequest{PackageId: id, Settings: &sourcev1.PackageSettings{EnabledLangs: langs[:1]}})
			if err != nil {
				return "", err
			}
			listed, err := w.listedOf()
			if err != nil {
				return "", err
			}
			for sid, lang := range listed {
				if lang != langs[0] {
					w.rep.problem("SetPackageSettings (%s): source %s (%s) is still listed with only %s enabled", id, sid, lang, langs[0])
				}
			}
			for _, ps := range installed.GetSources() {
				if ps.GetLang() != langs[0] {
					if _, err := w.c.GetSource(w.ctx, &sourcev1.GetSourceRequest{SourceId: ps.GetId()}); plugin.ErrorCode(err) != plugin.CodeNotFound {
						w.rep.problem("GetSource (%s): a source outside the enabled languages must be NOT_FOUND, got %v", ps.GetId(), err)
					}
				}
			}
			return fmt.Sprintf("%d sources in %s", len(listed), langs[0]), nil
		})
		w.step("Restore languages", func() (string, error) {
			_, err := w.c.SetPackageSettings(w.ctx, &sourcev1.SetPackageSettingsRequest{PackageId: id, Settings: original})
			return "", err
		})
	}
	if wasInstalled {
		return
	}
	w.step("UninstallPackage", func() (string, error) {
		if _, err := w.c.UninstallPackage(w.ctx, &sourcev1.UninstallPackageRequest{PackageId: id}); err != nil {
			return "", err
		}
		listed, err := w.listedOf()
		if err != nil {
			return "", err
		}
		if len(listed) > 0 {
			w.rep.problem("UninstallPackage (%s): its sources are still listed", id)
		}
		if _, err := w.c.UninstallPackage(w.ctx, &sourcev1.UninstallPackageRequest{PackageId: id}); err != nil {
			w.rep.problem("UninstallPackage (%s): uninstalling again must answer OK: %v", id, err)
		}
		return "", nil
	})
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}
