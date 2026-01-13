//go:build windows

package registry

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	winregistry "golang.org/x/sys/windows/registry"
)

var (
	modAdvapi32 = windows.NewLazySystemDLL("advapi32.dll")
	procRegRenameKey = modAdvapi32.NewProc("RegRenameKey")
)

func regRenameKey(key windows.Handle, oldName *uint16, newName *uint16) error {
	r1, _, _ := procRegRenameKey.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(oldName)),
		uintptr(unsafe.Pointer(newName)),
	)
	if r1 != 0 {
		return syscall.Errno(r1)
	}
	return nil
}

const registryAccess = winregistry.ENUMERATE_SUB_KEYS | winregistry.QUERY_VALUE | winregistry.SET_VALUE

type nativeProvider struct{}

func newNativeProvider() Provider {
	return &nativeProvider{}
}

func (p *nativeProvider) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{Enumerate: true, Mutate: true}
}

func (p *nativeProvider) List(ctx context.Context, req ListRequest) (RegistryListResult, error) {
	hiveNames := []string{"HKEY_LOCAL_MACHINE", "HKEY_CURRENT_USER", "HKEY_USERS"}
	if strings.TrimSpace(req.Hive) != "" {
		hiveNames = []string{req.Hive}
	}
	snapshot := make(RegistrySnapshot)
	for _, hiveName := range hiveNames {
		hiveData, err := p.enumerateHive(ctx, hiveName, strings.TrimSpace(req.Path), req.Depth)
		if err != nil {
			return RegistryListResult{}, err
		}
		snapshot[hiveName] = hiveData
	}
	return RegistryListResult{
		Snapshot:    snapshot,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) CreateKey(ctx context.Context, req CreateKeyRequest) (RegistryMutationResult, error) {
	hive := strings.TrimSpace(req.Hive)
	name := strings.TrimSpace(req.Name)
	if hive == "" || name == "" {
		return RegistryMutationResult{}, fmt.Errorf("registry hive and name required")
	}
	root, err := mapHiveName(hive)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	parentPath := normalizeRegistryPath(req.ParentPath)
	parentKey, closeParent, err := openWritableKey(root, parentPath)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	if closeParent {
		defer parentKey.Close()
	}

	newKey, _, err := winregistry.CreateKey(parentKey, name, winregistry.CREATE_SUB_KEY|registryAccess)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	newKey.Close()

	keyPath := joinRegistryPath(parentPath, name)
	hiveData, err := p.enumerateHive(ctx, hive, "", -1)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	return RegistryMutationResult{
		Hive:      hiveData,
		KeyPath:   keyPath,
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) CreateValue(ctx context.Context, req CreateValueRequest) (RegistryMutationResult, error) {
	hive := strings.TrimSpace(req.Hive)
	keyPath := normalizeRegistryPath(req.KeyPath)
	if hive == "" || keyPath == "" {
		return RegistryMutationResult{}, fmt.Errorf("registry hive and key path required")
	}
	root, err := mapHiveName(hive)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	key, err := winregistry.OpenKey(root, keyPath, registryAccess)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	defer key.Close()

	value := req.Value
	if err := writeRegistryValue(key, value); err != nil {
		return RegistryMutationResult{}, err
	}

	hiveData, err := p.enumerateHive(ctx, hive, "", -1)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	valueName := value.Name
	if valueName == "" {
		valueName = defaultValueName()
	}
	return RegistryMutationResult{
		Hive:      hiveData,
		KeyPath:   keyPath,
		ValueName: stringPointer(valueName),
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) UpdateKey(ctx context.Context, req UpdateKeyRequest) (RegistryMutationResult, error) {
	hive := strings.TrimSpace(req.Hive)
	path := normalizeRegistryPath(req.Path)
	name := strings.TrimSpace(req.Name)
	if hive == "" || path == "" || name == "" {
		return RegistryMutationResult{}, fmt.Errorf("registry hive, path, and name required")
	}
	root, err := mapHiveName(hive)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	parentPath, currentName := splitRegistryPath(path)
	parentKey, closeParent, err := openWritableKey(root, parentPath)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	if closeParent {
		defer parentKey.Close()
	}

	if strings.EqualFold(currentName, name) {
		hiveData, err := p.enumerateHive(ctx, hive, "", -1)
		if err != nil {
			return RegistryMutationResult{}, err
		}
		return RegistryMutationResult{
			Hive:      hiveData,
			KeyPath:   path,
			MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}, nil
	}

	oldPtr, err := windows.UTF16PtrFromString(currentName)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	newPtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	if err := regRenameKey(windows.Handle(parentKey), oldPtr, newPtr); err != nil {
		return RegistryMutationResult{}, fmt.Errorf("rename registry key: %w", err)
	}

	newPath := joinRegistryPath(parentPath, name)
	hiveData, err := p.enumerateHive(ctx, hive, "", -1)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	return RegistryMutationResult{
		Hive:      hiveData,
		KeyPath:   newPath,
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) UpdateValue(ctx context.Context, req UpdateValueRequest) (RegistryMutationResult, error) {
	hive := strings.TrimSpace(req.Hive)
	keyPath := normalizeRegistryPath(req.KeyPath)
	if hive == "" || keyPath == "" {
		return RegistryMutationResult{}, fmt.Errorf("registry hive and key path required")
	}
	root, err := mapHiveName(hive)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	key, err := winregistry.OpenKey(root, keyPath, registryAccess)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	defer key.Close()

	value := req.Value
	if err := writeRegistryValue(key, value); err != nil {
		return RegistryMutationResult{}, err
	}

	originalName := sanitizeValueName(req.OriginalName)
	newName := sanitizeValueName(value.Name)
	if originalName != "" && !strings.EqualFold(originalName, newName) {
		_ = key.DeleteValue(originalName)
	}

	hiveData, err := p.enumerateHive(ctx, hive, "", -1)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	displayName := value.Name
	if displayName == "" {
		displayName = defaultValueName()
	}
	return RegistryMutationResult{
		Hive:      hiveData,
		KeyPath:   keyPath,
		ValueName: stringPointer(displayName),
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) DeleteKey(ctx context.Context, req DeleteKeyRequest) (RegistryMutationResult, error) {
	hive := strings.TrimSpace(req.Hive)
	path := normalizeRegistryPath(req.Path)
	if hive == "" || path == "" {
		return RegistryMutationResult{}, fmt.Errorf("registry hive and key path required")
	}
	root, err := mapHiveName(hive)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	parentPath, name := splitRegistryPath(path)
	parentKey, closeParent, err := openWritableKey(root, parentPath)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	if closeParent {
		defer parentKey.Close()
	}

	if err := winregistry.DeleteKey(parentKey, name); err != nil {
		return RegistryMutationResult{}, err
	}

	hiveData, err := p.enumerateHive(ctx, hive, "", -1)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	fallback := parentPath
	hivePaths := hiveData
	if fallback != "" {
		if _, exists := hivePaths[fallback]; !exists {
			fallback = ""
		}
	}
	return RegistryMutationResult{
		Hive:      hiveData,
		KeyPath:   fallback,
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) DeleteValue(ctx context.Context, req DeleteValueRequest) (RegistryMutationResult, error) {
	hive := strings.TrimSpace(req.Hive)
	keyPath := normalizeRegistryPath(req.KeyPath)
	if hive == "" || keyPath == "" {
		return RegistryMutationResult{}, fmt.Errorf("registry hive and key path required")
	}
	root, err := mapHiveName(hive)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	key, err := winregistry.OpenKey(root, keyPath, registryAccess)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	defer key.Close()

	name := sanitizeValueName(req.Name)
	if err := key.DeleteValue(name); err != nil {
		return RegistryMutationResult{}, err
	}

	hiveData, err := p.enumerateHive(ctx, hive, "", -1)
	if err != nil {
		return RegistryMutationResult{}, err
	}
	return RegistryMutationResult{
		Hive:      hiveData,
		KeyPath:   keyPath,
		MutatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (p *nativeProvider) enumerateHive(ctx context.Context, hiveName, basePath string, depth int) (RegistryHive, error) {
	root, err := mapHiveName(hiveName)
	if err != nil {
		return nil, err
	}
	basePath = normalizeRegistryPath(basePath)
	hive := make(RegistryHive)
	if basePath != "" {
		key, err := winregistry.OpenKey(root, basePath, registryAccess)
		if err != nil {
			if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
				return hive, nil
			}
			return nil, err
		}
		defer key.Close()
		if err := p.collectKey(ctx, key, hiveName, basePath, depth, hive); err != nil {
			return nil, err
		}
		return hive, nil
	}

	rootKey := winregistry.Key(root)
	subKeys, err := rootKey.ReadSubKeyNames(-1)
	if err != nil {
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return hive, nil
		}
		return nil, err
	}
	nextDepth := depth
	if nextDepth == 0 {
		nextDepth = -1
	}
	for _, sub := range subKeys {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		subKey, err := winregistry.OpenKey(rootKey, sub, registryAccess)
		if err != nil {
			continue
		}
		if err := p.collectKey(ctx, subKey, hiveName, sub, nextDepth-1, hive); err != nil {
			subKey.Close()
			return nil, err
		}
		subKey.Close()
	}
	return hive, nil
}

func (p *nativeProvider) collectKey(ctx context.Context, key winregistry.Key, hiveName, path string, depth int, hive RegistryHive) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	info, err := key.Stat()
	if err != nil {
		return err
	}
	parent := parentPath(path)
	parentPtr := (*string)(nil)
	if parent != "" {
		parentPtr = stringPointer(parent)
	}
	entry := RegistryKey{
		Hive:          hiveName,
		Name:          lastSegment(path),
		Path:          path,
		ParentPath:    parentPtr,
		Values:        []RegistryValue{},
		SubKeys:       []string{},
		LastModified:  info.ModTime().UTC().Format(time.RFC3339Nano),
		Wow64Mirrored: false,
		Owner:         "SYSTEM",
	}
	if values, err := readRegistryValues(key, entry.LastModified); err == nil {
		entry.Values = values
	}
	if depth != 0 {
		subNames, err := key.ReadSubKeyNames(-1)
		if err != nil && !errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return err
		}
		nextDepth := depth - 1
		if depth < 0 {
			nextDepth = -1
		}
		for _, sub := range subNames {
			subPath := joinRegistryPath(path, sub)
			entry.SubKeys = append(entry.SubKeys, subPath)
			child, err := winregistry.OpenKey(key, sub, registryAccess)
			if err != nil {
				continue
			}
			if err := p.collectKey(ctx, child, hiveName, subPath, nextDepth, hive); err != nil {
				child.Close()
				return err
			}
			child.Close()
		}
	}
	hive[path] = entry
	return nil
}

func readRegistryValues(key winregistry.Key, lastModified string) ([]RegistryValue, error) {
	names, err := key.ReadValueNames(-1)
	if err != nil {
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return []RegistryValue{}, nil
		}
		return nil, err
	}
	values := make([]RegistryValue, 0, len(names))
	for _, name := range names {
		value, err := readRegistryValue(key, name, lastModified)
		if err != nil {
			continue
		}
		values = append(values, value)
	}
	return values, nil
}

func readRegistryValue(key winregistry.Key, name string, lastModified string) (RegistryValue, error) {
	valueType, _, err := key.GetValue(name, nil)
	if err != nil {
		return RegistryValue{}, err
	}
	displayName := name
	if displayName == "" {
		displayName = defaultValueName()
	}

	switch uint32(valueType) {
	case winregistry.SZ:
		data, _, err := key.GetStringValue(name)
		if err != nil {
			return RegistryValue{}, err
		}
		return RegistryValue{
			Name:         displayName,
			Type:         "REG_SZ",
			Data:         data,
			Size:         utf16Length(data) * 2,
			LastModified: lastModified,
		}, nil
	case winregistry.EXPAND_SZ:
		data, _, err := key.GetStringValue(name)
		if err != nil {
			return RegistryValue{}, err
		}
		return RegistryValue{
			Name:         displayName,
			Type:         "REG_EXPAND_SZ",
			Data:         data,
			Size:         utf16Length(data) * 2,
			LastModified: lastModified,
		}, nil
	case winregistry.MULTI_SZ:
		data, _, err := key.GetStringsValue(name)
		if err != nil {
			return RegistryValue{}, err
		}
		joined := strings.Join(data, "\n")
		size := 0
		for _, segment := range data {
			size += utf16Length(segment) + 1
		}
		if size == 0 {
			size = 1
		}
		return RegistryValue{
			Name:         displayName,
			Type:         "REG_MULTI_SZ",
			Data:         joined,
			Size:         size * 2,
			LastModified: lastModified,
		}, nil
	case winregistry.DWORD:
		value, _, err := key.GetIntegerValue(name)
		if err != nil {
			return RegistryValue{}, err
		}
		return RegistryValue{
			Name:         displayName,
			Type:         "REG_DWORD",
			Data:         fmt.Sprintf("0x%08X", uint32(value)),
			Size:         4,
			LastModified: lastModified,
		}, nil
	case winregistry.QWORD:
		value, _, err := key.GetIntegerValue(name)
		if err != nil {
			return RegistryValue{}, err
		}
		return RegistryValue{
			Name:         displayName,
			Type:         "REG_QWORD",
			Data:         fmt.Sprintf("0x%016X", value),
			Size:         8,
			LastModified: lastModified,
		}, nil
	case winregistry.BINARY:
		data, _, err := key.GetBinaryValue(name)
		if err != nil {
			return RegistryValue{}, err
		}
		return RegistryValue{
			Name:         displayName,
			Type:         "REG_BINARY",
			Data:         hexWithSpaces(data),
			Size:         len(data),
			LastModified: lastModified,
		}, nil
	default:
		data, _, err := key.GetBinaryValue(name)
		if err != nil {
			return RegistryValue{}, err
		}
		return RegistryValue{
			Name:         displayName,
			Type:         fmt.Sprintf("REG_%d", valueType),
			Data:         hexWithSpaces(data),
			Size:         len(data),
			LastModified: lastModified,
		}, nil
	}
}

func writeRegistryValue(key winregistry.Key, value RegistryValueInput) error {
	name := sanitizeValueName(value.Name)
	switch strings.ToUpper(strings.TrimSpace(value.Type)) {
	case "REG_SZ":
		return key.SetStringValue(name, value.Data)
	case "REG_EXPAND_SZ":
		return key.SetExpandStringValue(name, value.Data)
	case "REG_MULTI_SZ":
		segments := splitMultiString(value.Data)
		return key.SetStringsValue(name, segments)
	case "REG_DWORD":
		parsed, err := parseIntegerValue(value.Data, 32)
		if err != nil {
			return err
		}
		return key.SetDWordValue(name, uint32(parsed))
	case "REG_QWORD":
		parsed, err := parseIntegerValue(value.Data, 64)
		if err != nil {
			return err
		}
		return key.SetQWordValue(name, parsed)
	case "REG_BINARY":
		bytes, err := parseHexBytes(value.Data)
		if err != nil {
			return err
		}
		return key.SetBinaryValue(name, bytes)
	default:
		return fmt.Errorf("unsupported registry value type %q", value.Type)
	}
}

func mapHiveName(name string) (winregistry.Key, error) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "HKEY_LOCAL_MACHINE":
		return winregistry.LOCAL_MACHINE, nil
	case "HKEY_CURRENT_USER":
		return winregistry.CURRENT_USER, nil
	case "HKEY_USERS":
		return winregistry.USERS, nil
	default:
		return winregistry.Key(0), fmt.Errorf("unsupported registry hive %q", name)
	}
}

func openWritableKey(root winregistry.Key, path string) (winregistry.Key, bool, error) {
	if path == "" {
		return winregistry.Key(root), false, nil
	}
	key, err := winregistry.OpenKey(root, path, registryAccess)
	if err != nil {
		return 0, false, err
	}
	return key, true, nil
}

func normalizeRegistryPath(path string) string {
	trimmed := strings.TrimSpace(path)
	trimmed = strings.Trim(trimmed, "\\")
	return trimmed
}

func joinRegistryPath(parent, child string) string {
	if parent == "" {
		return strings.Trim(child, "\\")
	}
	return strings.Trim(parent, "\\") + "\\" + strings.Trim(child, "\\")
}

func splitRegistryPath(path string) (string, string) {
	trimmed := normalizeRegistryPath(path)
	if trimmed == "" {
		return "", ""
	}
	idx := strings.LastIndex(trimmed, "\\")
	if idx < 0 {
		return "", trimmed
	}
	return trimmed[:idx], trimmed[idx+1:]
}

func parentPath(path string) string {
	parent, _ := splitRegistryPath(path)
	return parent
}

func lastSegment(path string) string {
	_, name := splitRegistryPath(path)
	if name == "" {
		return path
	}
	return name
}

func defaultValueName() string {
	return "(Default)"
}

func sanitizeValueName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == defaultValueName() {
		return ""
	}
	return trimmed
}

func splitMultiString(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{""}
	}
	parts := strings.Split(value, "\n")
	for i, part := range parts {
		parts[i] = strings.TrimRight(part, "\r")
	}
	return parts
}

func parseIntegerValue(input string, bits int) (uint64, error) {
	trimmed := strings.TrimSpace(strings.ToLower(input))
	if strings.HasPrefix(trimmed, "0x") {
		return strconv.ParseUint(trimmed[2:], 16, bits)
	}
	return strconv.ParseUint(trimmed, 10, bits)
}

func parseHexBytes(input string) ([]byte, error) {
	sanitized := strings.ReplaceAll(input, " ", "")
	sanitized = strings.ReplaceAll(sanitized, "\n", "")
	sanitized = strings.ReplaceAll(sanitized, "\r", "")
	if len(sanitized)%2 != 0 {
		sanitized = "0" + sanitized
	}
	return hex.DecodeString(sanitized)
}

func hexWithSpaces(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	encoded := strings.ToUpper(hex.EncodeToString(data))
	builder := strings.Builder{}
	for i := 0; i < len(encoded); i += 2 {
		if i > 0 {
			builder.WriteByte(' ')
		}
		end := i + 2
		if end > len(encoded) {
			end = len(encoded)
		}
		builder.WriteString(encoded[i:end])
	}
	return builder.String()
}

func utf16Length(value string) int {
	if value == "" {
		return 0
	}
	return len(utf16.Encode([]rune(value)))
}
