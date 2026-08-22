package managed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

const DefaultMaxLocatorBytes = 64 << 10

var (
	preferencePattern = regexp.MustCompile(`^(best|balanced|fast)$`)
	packagePart       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]*$`)
	envNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func invalid(field, message string) error { return &InvalidSpecError{Field: field, Message: message} }

func (s SessionSpec) Validate() error {
	pref := strings.TrimSpace(s.Model.Preference)
	if (pref == "") == (s.Model.Exact == nil) {
		return invalid("model", "set exactly one of preference or exact")
	}
	if pref != "" && !preferencePattern.MatchString(pref) {
		return invalid("model.preference", "must be best, balanced, or fast")
	}
	if s.Model.Exact != nil && !s.Model.Exact.Valid() {
		return invalid("model.exact", "provider and id are required")
	}
	if strings.TrimSpace(s.Instructions) == "" {
		return invalid("instructions", "must not be empty")
	}
	if strings.TrimSpace(s.Task) == "" {
		return invalid("task", "must not be empty")
	}
	if s.Evaluation != nil {
		if strings.TrimSpace(s.Evaluation.Rubric) == "" {
			return invalid("evaluation.rubric", "must not be empty")
		}
		if s.Evaluation.MaxRevisions <= 0 {
			return invalid("evaluation.max_revisions", "must be positive")
		}
	}
	if err := s.Sandbox.validate(); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(s.Resources))
	for i, resource := range s.Resources {
		destination, err := resource.validate(i)
		if err != nil {
			return err
		}
		if _, exists := seen[destination]; exists {
			return invalid(fmt.Sprintf("resources[%d]", i), "duplicate destination "+destination)
		}
		seen[destination] = struct{}{}
	}
	return nil
}

func (s SandboxSpec) validate() error {
	for i, pkg := range s.Packages {
		if !packagePart.MatchString(pkg.Manager) {
			return invalid(fmt.Sprintf("sandbox.packages[%d].manager", i), "invalid package manager")
		}
		if !packagePart.MatchString(pkg.Name) {
			return invalid(fmt.Sprintf("sandbox.packages[%d].name", i), "invalid package name")
		}
		if pkg.Version != "" && !packagePart.MatchString(pkg.Version) {
			return invalid(fmt.Sprintf("sandbox.packages[%d].version", i), "invalid package version")
		}
	}
	for i, argv := range s.Bootstrap {
		if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
			return invalid(fmt.Sprintf("sandbox.bootstrap[%d]", i), "argv must contain an executable")
		}
		for _, arg := range argv {
			if strings.ContainsRune(arg, 0) {
				return invalid(fmt.Sprintf("sandbox.bootstrap[%d]", i), "argv contains NUL")
			}
		}
	}
	mode := s.Network.Mode
	if mode == "" {
		mode = NetworkNone
	}
	switch mode {
	case NetworkNone:
		if len(s.Network.AllowedDomains) > 0 || len(s.Network.SecretRefs) > 0 {
			return invalid("sandbox.network", "none cannot carry domains or secrets")
		}
	case NetworkAllowlist:
		if len(s.Network.AllowedDomains) == 0 {
			return invalid("sandbox.network.allowed_domains", "allowlist requires a domain")
		}
	case NetworkOpen:
		if len(s.Network.AllowedDomains) > 0 || len(s.Network.SecretRefs) > 0 {
			return invalid("sandbox.network", "open cannot carry domains or host-bound secrets")
		}
	default:
		return invalid("sandbox.network.mode", "unknown network mode")
	}
	allowed := make(map[string]struct{}, len(s.Network.AllowedDomains))
	for i, domain := range s.Network.AllowedDomains {
		canonical, err := canonicalDomain(domain)
		if err != nil || canonical != domain {
			return invalid(fmt.Sprintf("sandbox.network.allowed_domains[%d]", i), "must be a canonical hostname")
		}
		allowed[domain] = struct{}{}
	}
	for envName, ref := range s.Network.SecretRefs {
		if !envNamePattern.MatchString(envName) || strings.TrimSpace(ref) == "" {
			return invalid("sandbox.network.secret_refs", "invalid environment name or empty reference")
		}
		domain, _, ok := strings.Cut(ref, ":")
		if !ok {
			return invalid("sandbox.network.secret_refs", "reference must be domain:name")
		}
		if _, ok := allowed[domain]; !ok {
			return invalid("sandbox.network.secret_refs", "secret domain is not allowlisted")
		}
	}
	return nil
}

func canonicalDomain(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "/:@") {
		return "", fmt.Errorf("not a hostname")
	}
	host := strings.ToLower(strings.TrimSuffix(raw, "."))
	if net.ParseIP(host) != nil {
		return "", fmt.Errorf("IP addresses are not domains")
	}
	u, err := url.Parse("https://" + host)
	if err != nil || u.Hostname() != host || !strings.Contains(host, ".") {
		return "", fmt.Errorf("invalid hostname")
	}
	return host, nil
}

func (r Resource) validate(index int) (string, error) {
	field := fmt.Sprintf("resources[%d]", index)
	switch r.Kind {
	case ResourceAttachment:
		if r.Attachment == nil || r.Repository != nil {
			return "", invalid(field, "attachment payload mismatch")
		}
		if err := safeRelativePath(r.Attachment.Path); err != nil {
			return "", invalid(field+".attachment.path", err.Error())
		}
		digest, err := hex.DecodeString(r.Attachment.SHA256)
		if err != nil || len(digest) != sha256.Size {
			return "", invalid(field+".attachment.sha256", "must be a SHA-256 hex digest")
		}
		actual := sha256.Sum256(r.Attachment.Content)
		if !strings.EqualFold(hex.EncodeToString(actual[:]), r.Attachment.SHA256) {
			return "", invalid(field+".attachment.sha256", "content digest mismatch")
		}
		return path.Clean(r.Attachment.Path), nil
	case ResourceRepository:
		if r.Repository == nil || r.Attachment != nil {
			return "", invalid(field, "repository payload mismatch")
		}
		u, err := url.Parse(r.Repository.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", invalid(field+".repository.url", "must be an HTTPS URL")
		}
		if err := safeRelativePath(r.Repository.Destination); err != nil {
			return "", invalid(field+".repository.destination", err.Error())
		}
		if r.Repository.Access != AccessReadOnly && r.Repository.Access != AccessReadWrite {
			return "", invalid(field+".repository.access", "must be ro or rw")
		}
		if r.Repository.Access == AccessReadWrite && strings.TrimSpace(r.Repository.CredentialRef) == "" {
			return "", invalid(field+".repository.credential_ref", "read-write repository requires a credential reference")
		}
		return path.Clean(r.Repository.Destination), nil
	default:
		return "", invalid(field+".kind", "unknown resource kind")
	}
}

func safeRelativePath(value string) error {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return fmt.Errorf("must be a non-empty relative slash path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != value {
		return fmt.Errorf("must be a clean contained path")
	}
	return nil
}

func (r SessionRef) Validate(maxBytes int) error {
	if CanonicalRuntimeName(string(r.Runtime)) == "" || r.Runtime != CanonicalRuntimeName(string(r.Runtime)) {
		return invalid("ref.runtime", "must be canonical and non-empty")
	}
	if strings.TrimSpace(r.ID) == "" {
		return invalid("ref.id", "must not be empty")
	}
	if r.Revision == 0 {
		return invalid("ref.revision", "must be positive")
	}
	if r.LocatorVersion <= 0 {
		return invalid("ref.locator_version", "must be positive")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLocatorBytes
	}
	if len(r.Locator) > maxBytes {
		return invalid("ref.locator", "exceeds maximum size")
	}
	if len(r.Locator) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(r.Locator, &value); err != nil {
		return invalid("ref.locator", "must be valid JSON")
	}
	if secretKey(value) {
		return invalid("ref.locator", "contains a secret-bearing key")
	}
	return nil
}

func secretKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			for _, forbidden := range []string{"secret", "token", "password", "credential", "api_key", "apikey"} {
				if strings.Contains(lower, forbidden) {
					return true
				}
			}
			if secretKey(child) {
				return true
			}
		}
	case []any:
		if slices.ContainsFunc(typed, secretKey) {
			return true
		}
	}
	return false
}

func ValidateCapabilities(runtime RuntimeName, capabilities Capabilities, spec SessionSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	for i, pkg := range spec.Sandbox.Packages {
		if !capabilities.PackageManagers[pkg.Manager] {
			return &UnsupportedError{Runtime: runtime, Field: fmt.Sprintf("sandbox.packages[%d].manager", i), Capability: "package_manager:" + pkg.Manager}
		}
	}
	mode := spec.Sandbox.Network.Mode
	if mode == "" {
		mode = NetworkNone
	}
	if !capabilities.NetworkModes[mode] {
		return &UnsupportedError{Runtime: runtime, Field: "sandbox.network.mode", Capability: "network:" + string(mode)}
	}
	if spec.Evaluation != nil && !capabilities.Evaluation {
		return &UnsupportedError{Runtime: runtime, Field: "evaluation", Capability: "evaluation"}
	}
	for i, resource := range spec.Resources {
		if resource.Kind == ResourceRepository && !capabilities.RepositoryModes[resource.Repository.Access] {
			return &UnsupportedError{Runtime: runtime, Field: fmt.Sprintf("resources[%d].repository.access", i), Capability: "repository:" + string(resource.Repository.Access)}
		}
		if resource.Kind == ResourceAttachment && capabilities.MaxAttachmentBytes > 0 && int64(len(resource.Attachment.Content)) > capabilities.MaxAttachmentBytes {
			return &UnsupportedError{Runtime: runtime, Field: fmt.Sprintf("resources[%d].attachment.content", i), Capability: "attachment_size"}
		}
	}
	return nil
}
