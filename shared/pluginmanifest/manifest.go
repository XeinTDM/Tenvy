package pluginmanifest

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

type Manifest struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Version       string             `json:"version"`
	Description   string             `json:"description,omitempty"`
	Entry         string             `json:"entry"`
	Author        string             `json:"author,omitempty"`
	Homepage      string             `json:"homepage,omitempty"`
	RepositoryURL string             `json:"repositoryUrl,omitempty"`
	License       *LicenseInfo       `json:"license,omitempty"`
	Categories    []string           `json:"categories,omitempty"`
	Capabilities  []string           `json:"capabilities,omitempty"`
	Telemetry     []string           `json:"telemetry,omitempty"`
	Dependencies  []string           `json:"dependencies,omitempty"`
	Runtime       *RuntimeDescriptor `json:"runtime,omitempty"`
	Requirements  Requirements       `json:"requirements"`
	Distribution  Distribution       `json:"distribution"`
	Package       PackageDescriptor  `json:"package"`
}

type CapabilityMetadata struct {
	ID          string
	Module      string
	Name        string
	Description string
}

type TelemetryMetadata struct {
	ID          string
	Module      string
	Name        string
	Description string
}

type moduleCapabilityDescriptor struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type moduleDefinition struct {
	ID           string                       `json:"id"`
	Capabilities []moduleCapabilityDescriptor `json:"capabilities"`
	Telemetry    []moduleCapabilityDescriptor `json:"telemetry"`
}

type moduleDefinitionsPayload struct {
	Modules []moduleDefinition `json:"modules"`
}

//go:embed definitions.json
var moduleDefinitionsJSON []byte

func init() {
	var payload moduleDefinitionsPayload
	if err := json.Unmarshal(moduleDefinitionsJSON, &payload); err != nil {
		panic(fmt.Sprintf("parse module definitions: %v", err))
	}

	for _, module := range payload.Modules {
		moduleID := strings.TrimSpace(module.ID)
		if moduleID == "" {
			continue
		}
		registeredModules[moduleID] = struct{}{}

		for _, capability := range module.Capabilities {
			capID := strings.TrimSpace(capability.ID)
			if capID == "" {
				continue
			}
			normalized := strings.ToLower(capID)
			if prev, ok := normalizedCapabilities[normalized]; ok {
				panic(fmt.Sprintf("duplicate capability id %q found in module %s (previously registered by %s)", capID, moduleID, prev))
			}
			registeredCapabilities[capID] = CapabilityMetadata{
				ID:          capID,
				Module:      moduleID,
				Name:        strings.TrimSpace(capability.Name),
				Description: strings.TrimSpace(capability.Description),
			}
			normalizedCapabilities[normalized] = moduleID
		}

		for _, telemetry := range module.Telemetry {
			teleID := strings.TrimSpace(telemetry.ID)
			if teleID == "" {
				continue
			}
			normalized := strings.ToLower(teleID)
			if prev, ok := normalizedTelemetry[normalized]; ok {
				panic(fmt.Sprintf("duplicate telemetry id %q found in module %s (previously registered by %s)", teleID, moduleID, prev))
			}
			registeredTelemetry[teleID] = TelemetryMetadata{
				ID:          teleID,
				Module:      moduleID,
				Name:        strings.TrimSpace(telemetry.Name),
				Description: strings.TrimSpace(telemetry.Description),
			}
			normalizedTelemetry[normalized] = moduleID
		}
	}
}

type Requirements struct {
	MinAgentVersion  string               `json:"minAgentVersion,omitempty"`
	MaxAgentVersion  string               `json:"maxAgentVersion,omitempty"`
	MinClientVersion string               `json:"minClientVersion,omitempty"`
	Platforms        []PluginPlatform     `json:"platforms,omitempty"`
	Architectures    []PluginArchitecture `json:"architectures,omitempty"`
	RequiredModules  []string             `json:"requiredModules,omitempty"`
}

type Distribution struct {
	DefaultMode               DeliveryMode  `json:"defaultMode"`
	AutoUpdate                bool          `json:"autoUpdate"`
	Signature                 SignatureType `json:"signature"`
	SignatureHash             string        `json:"signatureHash,omitempty"`
	SignatureValue            string        `json:"signatureValue,omitempty"`
	SignatureTimestamp        string        `json:"signatureTimestamp,omitempty"`
	SignatureSigner           string        `json:"signatureSigner,omitempty"`
	SignatureCertificateChain []string      `json:"signatureCertificateChain,omitempty"`
}

type RuntimeDescriptor struct {
	Type      RuntimeType          `json:"type"`
	Sandboxed bool                 `json:"sandboxed,omitempty"`
	Host      *RuntimeHostContract `json:"host,omitempty"`
}

type RuntimeHostContract struct {
	APIVersion string   `json:"apiVersion,omitempty"`
	Interfaces []string `json:"interfaces,omitempty"`
}

type PackageDescriptor struct {
	Artifact  string `json:"artifact"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	Hash      string `json:"hash,omitempty"`
}

type LicenseInfo struct {
	SPDXID string `json:"spdxId"`
	Name   string `json:"name,omitempty"`
	URL    string `json:"url,omitempty"`
}

type (
	DeliveryMode         string
	SignatureType        string
	PluginPlatform       string
	PluginArchitecture   string
	PluginInstallStatus  string
	PluginApprovalStatus string
	RuntimeType          string
)

const (
	DeliveryManual    DeliveryMode = "manual"
	DeliveryAutomatic DeliveryMode = "automatic"

	SignatureSHA256  SignatureType = "sha256"
	SignatureEd25519 SignatureType = "ed25519"

	PlatformWindows PluginPlatform = "windows"
	PlatformLinux   PluginPlatform = "linux"
	PlatformMacOS   PluginPlatform = "macos"

	ArchitectureX8664 PluginArchitecture = "x86_64"
	ArchitectureARM64 PluginArchitecture = "arm64"

	InstallInstalled PluginInstallStatus = "installed"
	InstallBlocked   PluginInstallStatus = "blocked"
	InstallError     PluginInstallStatus = "error"
	InstallDisabled  PluginInstallStatus = "disabled"

	ApprovalPending  PluginApprovalStatus = "pending"
	ApprovalApproved PluginApprovalStatus = "approved"
	ApprovalRejected PluginApprovalStatus = "rejected"

	RuntimeNative RuntimeType = "native"
	RuntimeWASM   RuntimeType = "wasm"

	HostInterfaceCoreV1 = "tenvy.core/1"
)

var (
	knownDeliveryModes     = []DeliveryMode{DeliveryManual, DeliveryAutomatic}
	knownSignatureTypes    = []SignatureType{SignatureSHA256, SignatureEd25519}
	knownPlatforms         = []PluginPlatform{PlatformWindows, PlatformLinux, PlatformMacOS}
	knownArchitectures     = []PluginArchitecture{ArchitectureX8664, ArchitectureARM64}
	knownInstallStates     = []PluginInstallStatus{InstallBlocked, InstallDisabled, InstallError, InstallInstalled}
	knownApprovalStates    = []PluginApprovalStatus{ApprovalPending, ApprovalApproved, ApprovalRejected}
	knownRuntimeTypes      = []RuntimeType{RuntimeNative, RuntimeWASM}
	semverPattern          = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-.]+)?(?:\+[0-9A-Za-z-.]+)?$`)
	registeredModules      = map[string]struct{}{}
	registeredCapabilities = map[string]CapabilityMetadata{}
	registeredTelemetry    = map[string]TelemetryMetadata{}
	normalizedCapabilities = map[string]string{}
	normalizedTelemetry    = map[string]string{}
)

func LookupTelemetry(id string) (TelemetryMetadata, bool) {
	descriptor, ok := registeredTelemetry[strings.TrimSpace(id)]
	return descriptor, ok
}

type InstallationTelemetry struct {
	PluginID  string              `json:"pluginId" msgpack:"pluginId"`
	Version   string              `json:"version" msgpack:"version"`
	Status    PluginInstallStatus `json:"status" msgpack:"status"`
	Hash      string              `json:"hash,omitempty" msgpack:"hash,omitempty"`
	Timestamp *int64              `json:"timestamp,omitempty" msgpack:"timestamp,omitempty"`
	Error     string              `json:"error,omitempty" msgpack:"error,omitempty"`
}

type SyncPayload struct {
	Installations []InstallationTelemetry `json:"installations" msgpack:"installations"`
	Manifests     *ManifestState          `json:"manifests,omitempty" msgpack:"manifests,omitempty"`
}

type ManifestDescriptor struct {
	PluginID       string           `json:"pluginId" msgpack:"pluginId"`
	Version        string           `json:"version" msgpack:"version"`
	ManifestDigest string           `json:"manifestDigest" msgpack:"manifestDigest"`
	ArtifactHash   string           `json:"artifactHash,omitempty" msgpack:"artifactHash,omitempty"`
	ArtifactSize   int64            `json:"artifactSizeBytes,omitempty" msgpack:"artifactSizeBytes,omitempty"`
	ApprovedAt     string           `json:"approvedAt,omitempty" msgpack:"approvedAt,omitempty"`
	ManualPushAt   string           `json:"manualPushAt,omitempty" msgpack:"manualPushAt,omitempty"`
	Dependencies   []string         `json:"dependencies,omitempty" msgpack:"dependencies,omitempty"`
	Distribution   ManifestBriefing `json:"distribution" msgpack:"distribution"`
}

type ManifestBriefing struct {
	DefaultMode DeliveryMode `json:"defaultMode" msgpack:"defaultMode"`
	AutoUpdate  bool         `json:"autoUpdate" msgpack:"autoUpdate"`
}

type ManifestList struct {
	Version   string               `json:"version" msgpack:"version"`
	Manifests []ManifestDescriptor `json:"manifests" msgpack:"manifests"`
}

type ManifestState struct {
	Version string            `json:"version,omitempty" msgpack:"version,omitempty"`
	Digests map[string]string `json:"digests,omitempty" msgpack:"digests,omitempty"`
}

type ManifestDelta struct {
	Version string               `json:"version" msgpack:"version"`
	Updated []ManifestDescriptor `json:"updated" msgpack:"updated"`
	Removed []string             `json:"removed" msgpack:"removed"`
}

func (m Manifest) Validate() error {
	var problems []error

	if strings.TrimSpace(m.ID) == "" {
		problems = append(problems, errors.New("missing id"))
	}
	if strings.TrimSpace(m.Name) == "" {
		problems = append(problems, errors.New("missing name"))
	}
	if version := strings.TrimSpace(m.Version); version == "" {
		problems = append(problems, errors.New("missing version"))
	} else if !semverPattern.MatchString(version) {
		problems = append(problems, fmt.Errorf("invalid semantic version: %s", m.Version))
	}
	if strings.TrimSpace(m.Entry) == "" {
		problems = append(problems, errors.New("missing entry"))
	}
	if runtimeErrs := m.validateRuntime(); len(runtimeErrs) > 0 {
		problems = append(problems, runtimeErrs...)
	}
	if err := validateRepositoryURL(m.RepositoryURL); err != nil {
		problems = append(problems, err)
	}
	if err := m.validateLicense(); err != nil {
		problems = append(problems, err)
	}
	artifact := strings.TrimSpace(m.Package.Artifact)
	if artifact == "" {
		problems = append(problems, errors.New("missing package artifact"))
	} else if strings.ContainsAny(artifact, "/\\") {
		problems = append(problems, errors.New("package artifact must be a file name"))
	}

	if err := m.validateDistribution(); err != nil {
		problems = append(problems, err)
	}

	for index, module := range m.Requirements.RequiredModules {
		if strings.TrimSpace(module) == "" {
			problems = append(problems, fmt.Errorf("required module %d is empty", index))
			continue
		}
		if _, ok := registeredModules[module]; !ok {
			problems = append(problems, fmt.Errorf("required module %s is not registered", module))
		}
	}

	dependencySeen := make(map[string]struct{})
	manifestID := strings.ToLower(strings.TrimSpace(m.ID))
	for index, dependency := range m.Dependencies {
		trimmed := strings.TrimSpace(dependency)
		if trimmed == "" {
			problems = append(problems, fmt.Errorf("dependency %d is empty", index))
			continue
		}
		lowered := strings.ToLower(trimmed)
		if lowered == manifestID && lowered != "" {
			problems = append(problems, fmt.Errorf("dependency %s cannot reference the plugin itself", trimmed))
			continue
		}
		if _, ok := dependencySeen[lowered]; ok {
			problems = append(problems, fmt.Errorf("dependency %s is duplicated", trimmed))
			continue
		}
		dependencySeen[lowered] = struct{}{}
	}

	for index, capabilityID := range m.Capabilities {
		trimmed := strings.TrimSpace(capabilityID)
		if trimmed == "" {
			problems = append(problems, fmt.Errorf("capability %d is empty", index))
			continue
		}
		descriptor, ok := LookupCapability(trimmed)
		if !ok {
			problems = append(problems, fmt.Errorf("capability %s is not registered", trimmed))
			continue
		}
		if descriptor.Module == "" {
			continue
		}
		if _, ok := registeredModules[descriptor.Module]; !ok {
			problems = append(problems, fmt.Errorf("capability %s references unknown module %s", descriptor.ID, descriptor.Module))
		}
	}

	for index, telemetryID := range m.Telemetry {
		trimmed := strings.TrimSpace(telemetryID)
		if trimmed == "" {
			problems = append(problems, fmt.Errorf("telemetry %d is empty", index))
			continue
		}
		descriptor, ok := LookupTelemetry(trimmed)
		if !ok {
			problems = append(problems, fmt.Errorf("telemetry %s is not registered", trimmed))
			continue
		}
		if descriptor.Module == "" {
			continue
		}
		if _, ok := registeredModules[descriptor.Module]; !ok {
			problems = append(problems, fmt.Errorf("telemetry %s references unknown module %s", descriptor.ID, descriptor.Module))
		}
	}

	if err := validateSemverConstraint("minAgentVersion", m.Requirements.MinAgentVersion); err != nil {
		problems = append(problems, err)
	}
	if err := validateSemverConstraint("maxAgentVersion", m.Requirements.MaxAgentVersion); err != nil {
		problems = append(problems, err)
	}
	if err := validateSemverConstraint("minClientVersion", m.Requirements.MinClientVersion); err != nil {
		problems = append(problems, err)
	}

	for _, platform := range m.Requirements.Platforms {
		if !containsPlatform(platform) {
			problems = append(problems, fmt.Errorf("unsupported platform: %s", platform))
		}
	}

	for _, arch := range m.Requirements.Architectures {
		if !containsArchitecture(arch) {
			problems = append(problems, fmt.Errorf("unsupported architecture: %s", arch))
		}
	}

	return errors.Join(problems...)
}

func (m Manifest) validateDistribution() error {
	mode := strings.TrimSpace(string(m.Distribution.DefaultMode))
	if mode == "" {
		return errors.New("distribution default mode is required")
	}
	if !containsDeliveryMode(DeliveryMode(mode)) {
		return fmt.Errorf("unsupported delivery mode: %s", mode)
	}

	sigType := strings.TrimSpace(string(m.Distribution.Signature))
	if sigType == "" {
		return errors.New("distribution signature is required")
	}
	if !containsSignatureType(SignatureType(sigType)) {
		return fmt.Errorf("unsupported signature type: %s", sigType)
	}

	packageHash := strings.TrimSpace(m.Package.Hash)
	if packageHash == "" {
		return errors.New("signed packages must include a hash")
	}

	if sigHash := strings.TrimSpace(m.Distribution.SignatureHash); sigHash != "" && !strings.EqualFold(sigHash, packageHash) {
		return errors.New("signature hash does not match package hash")
	}

	if SignatureType(sigType) == SignatureEd25519 {
		if strings.TrimSpace(m.Distribution.SignatureSigner) == "" {
			return errors.New("ed25519 signatures require a signer id")
		}
		if strings.TrimSpace(m.Distribution.SignatureValue) == "" {
			return errors.New("ed25519 signatures require a signature value")
		}
	}

	return nil
}

func (m Manifest) validateRuntime() []error {
	descriptor := m.Runtime
	if descriptor == nil {
		return nil
	}

	var problems []error
	runtimeType := strings.TrimSpace(string(descriptor.Type))
	if runtimeType != "" {
		normalized := RuntimeType(strings.ToLower(runtimeType))
		if !containsRuntimeType(normalized) {
			problems = append(problems, fmt.Errorf("unsupported runtime type: %s", descriptor.Type))
		}
	}

	if descriptor.Host != nil {
		apiVersion := strings.TrimSpace(descriptor.Host.APIVersion)
		if apiVersion != "" && len(apiVersion) < 2 {
			problems = append(problems, fmt.Errorf("runtime host apiVersion is invalid: %s", descriptor.Host.APIVersion))
		}
		for index, iface := range descriptor.Host.Interfaces {
			if strings.TrimSpace(iface) == "" {
				problems = append(problems, fmt.Errorf("runtime host interface %d is empty", index))
			}
		}
	}

	return problems
}

func (m Manifest) validateLicense() error {
	if m.License == nil {
		return nil
	}
	if strings.TrimSpace(m.License.SPDXID) == "" {
		return errors.New("license requires spdxId")
	}
	if trimmed := strings.TrimSpace(m.License.URL); trimmed != "" {
		parsed, err := url.Parse(trimmed)
		if err != nil || !parsed.IsAbs() {
			return fmt.Errorf("license url invalid: %s", m.License.URL)
		}
	}
	return nil
}

func (m Manifest) RuntimeType() RuntimeType {
	if m.Runtime == nil {
		return RuntimeNative
	}
	raw := strings.ToLower(strings.TrimSpace(string(m.Runtime.Type)))
	switch raw {
	case string(RuntimeWASM):
		return RuntimeWASM
	case string(RuntimeNative):
		return RuntimeNative
	case "":
		return RuntimeNative
	default:
		return RuntimeNative
	}
}

func (m Manifest) RuntimeSandboxed() bool {
	if m.Runtime == nil {
		return false
	}
	return m.Runtime.Sandboxed
}

func (m Manifest) RuntimeHostInterfaces() []string {
	if m.Runtime == nil || m.Runtime.Host == nil {
		return nil
	}
	return sanitizeStringSlice(m.Runtime.Host.Interfaces)
}

func (m Manifest) RuntimeHostAPIVersion() string {
	if m.Runtime == nil || m.Runtime.Host == nil {
		return ""
	}
	return strings.TrimSpace(m.Runtime.Host.APIVersion)
}

func (m Manifest) DependenciesList() []string {
	return sanitizeStringSlice(m.Dependencies)
}

func LookupCapability(id string) (CapabilityMetadata, bool) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return CapabilityMetadata{}, false
	}
	if descriptor, ok := registeredCapabilities[trimmed]; ok {
		return descriptor, true
	}
	lowered := strings.ToLower(trimmed)
	if descriptor, ok := registeredCapabilities[lowered]; ok {
		return descriptor, true
	}
	return CapabilityMetadata{}, false
}

func validateRepositoryURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("repositoryUrl invalid: %v", err)
	}
	if !parsed.IsAbs() {
		return errors.New("repositoryUrl must be an absolute URL")
	}
	if parsed.Scheme != "https" {
		return errors.New("repositoryUrl must use https")
	}
	return nil
}

func containsDeliveryMode(candidate DeliveryMode) bool {
	return containsValue(candidate, knownDeliveryModes)
}

func containsSignatureType(candidate SignatureType) bool {
	return containsValue(candidate, knownSignatureTypes)
}

func containsPlatform(candidate PluginPlatform) bool {
	return containsValue(candidate, knownPlatforms)
}

func containsArchitecture(candidate PluginArchitecture) bool {
	return containsValue(candidate, knownArchitectures)
}

func containsInstallStatus(candidate PluginInstallStatus) bool {
	return containsValue(candidate, knownInstallStates)
}

func containsApprovalStatus(candidate PluginApprovalStatus) bool {
	return containsValue(candidate, knownApprovalStates)
}

func containsRuntimeType(candidate RuntimeType) bool {
	return containsValue(candidate, knownRuntimeTypes)
}

func containsValue[T comparable](candidate T, values []T) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func validateSemverConstraint(field string, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if !semverPattern.MatchString(trimmed) {
		return fmt.Errorf("invalid %s: %s", field, value)
	}
	return nil
}

func sanitizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		lowered := strings.ToLower(trimmed)
		if _, ok := seen[lowered]; ok {
			continue
		}
		seen[lowered] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}
	sort.Strings(normalized)
	return normalized
}

func init() {
	sort.Slice(knownDeliveryModes, func(i, j int) bool { return knownDeliveryModes[i] < knownDeliveryModes[j] })
	sort.Slice(knownSignatureTypes, func(i, j int) bool { return knownSignatureTypes[i] < knownSignatureTypes[j] })
	sort.Slice(knownPlatforms, func(i, j int) bool { return knownPlatforms[i] < knownPlatforms[j] })
	sort.Slice(knownArchitectures, func(i, j int) bool { return knownArchitectures[i] < knownArchitectures[j] })
	sort.Slice(knownInstallStates, func(i, j int) bool { return knownInstallStates[i] < knownInstallStates[j] })
	sort.Slice(knownApprovalStates, func(i, j int) bool { return knownApprovalStates[i] < knownApprovalStates[j] })
	sort.Slice(knownRuntimeTypes, func(i, j int) bool { return knownRuntimeTypes[i] < knownRuntimeTypes[j] })
}
