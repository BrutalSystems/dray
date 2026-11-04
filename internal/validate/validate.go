// Package validate checks rendered manifests against Kubernetes' own schemas
// with kubeconform, before anything reaches a cluster: a misspelled field or a
// wrong type is caught by Dray, not half-way through an apply.
package validate

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yannh/kubeconform/pkg/validator"
)

// Finding is one problem with one object in a manifest.
type Finding struct {
	// File is the manifest's path in the repo.
	File string
	// Object is Kind/name, when it could be read.
	Object string
	// Message says what is wrong.
	Message string
	// Error findings stop a deploy; others are warnings.
	Error bool
}

func (f Finding) String() string {
	where := f.File
	if f.Object != "" {
		where += " " + f.Object
	}
	return where + ": " + f.Message
}

// DefaultSchemas are where schemas come from: Kubernetes' built-in kinds, and
// a community catalog for common custom resources (cert-manager and the like).
var DefaultSchemas = []string{
	"default",
	"https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json",
}

// Schemas checks manifests against schemas.
type Schemas struct {
	v validator.Validator
}

// Options configure schema checking.
type Options struct {
	// KubernetesVersion picks the schemas, such as 1.36.0; empty is the
	// newest.
	KubernetesVersion string
	// Locations override DefaultSchemas.
	Locations []string
	// CacheDir keeps downloaded schemas; empty is the user's cache folder.
	CacheDir string
}

// New returns a schema checker. Fields a schema does not know are errors
// (strict), so a typo is never silently dropped.
func New(o Options) (*Schemas, error) {
	locs := o.Locations
	if len(locs) == 0 {
		locs = DefaultSchemas
	}
	cache := o.CacheDir
	if cache == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			cache = filepath.Join(dir, "dray", "schemas")
		}
	}
	if cache != "" {
		if err := os.MkdirAll(cache, 0o750); err != nil {
			return nil, err
		}
	}
	version := strings.TrimPrefix(o.KubernetesVersion, "v")
	if version == "" {
		version = "master"
	}
	v, err := validator.New(locs, validator.Opts{
		Cache:                cache,
		KubernetesVersion:    version,
		Strict:               true,
		IgnoreMissingSchemas: false,
	})
	if err != nil {
		return nil, err
	}
	return &Schemas{v: v}, nil
}

// Check checks one manifest file's documents. A kind with no schema anywhere
// is a warning: the cluster still checks it when it is applied.
func (s *Schemas) Check(file string, content []byte) []Finding {
	var out []Finding
	for _, r := range s.v.Validate(file, io.NopCloser(bytes.NewReader(content))) {
		obj := ""
		if sig, err := r.Resource.Signature(); err == nil && sig != nil {
			obj = sig.Kind + "/" + sig.Name
		}
		switch r.Status {
		case validator.Valid, validator.Empty:
		case validator.Skipped:
		case validator.Invalid:
			for _, ve := range r.ValidationErrors {
				msg := ve.Msg
				if ve.Path != "" {
					msg = ve.Path + ": " + msg
				}
				out = append(out, Finding{File: file, Object: obj, Message: msg, Error: true})
			}
			if len(r.ValidationErrors) == 0 && r.Err != nil {
				out = append(out, Finding{File: file, Object: obj, Message: r.Err.Error(), Error: true})
			}
		case validator.Error:
			msg := fmt.Sprint(r.Err)
			if isMissingSchema(r.Err) {
				out = append(out, Finding{File: file, Object: obj, Message: "no schema for this kind; not checked", Error: false})
				continue
			}
			out = append(out, Finding{File: file, Object: obj, Message: msg, Error: true})
		}
	}
	return out
}

// isMissingSchema is kubeconform's "could not find schema for <kind>"; it has
// no typed error for it.
func isMissingSchema(err error) bool {
	return err != nil && strings.Contains(err.Error(), "could not find schema")
}
