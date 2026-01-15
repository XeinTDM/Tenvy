package filemanagerengine

import "io"

type FileSystemEntryType string

const (
	EntryTypeFile    FileSystemEntryType = "file"
	EntryTypeDir     FileSystemEntryType = "directory"
	EntryTypeSymlink FileSystemEntryType = "symlink"
	EntryTypeOther   FileSystemEntryType = "other"
)

type FileSystemEntry struct {
	Name       string              `json:"name"`
	Path       string              `json:"path"`
	Type       FileSystemEntryType `json:"type"`
	Size       *int64              `json:"size"`
	ModifiedAt string              `json:"modifiedAt"`
	IsHidden   bool                `json:"isHidden"`
}

type DirectoryListing struct {
	Type    string            `json:"type"`
	Root    string            `json:"root"`
	Path    string            `json:"path"`
	Parent  *string           `json:"parent"`
	Entries []FileSystemEntry `json:"entries"`
}

type FileEncoding string

const (
	EncodingUTF8   FileEncoding = "utf-8"
	EncodingBase64 FileEncoding = "base64"
)

type FileContent struct {
	Type       string       `json:"type"`
	Root       string       `json:"root"`
	Path       string       `json:"path"`
	Name       string       `json:"name"`
	Size       int64        `json:"size"`
	ModifiedAt string       `json:"modifiedAt"`
	Encoding   FileEncoding `json:"encoding"`
	Content    string       `json:"content,omitempty"`
	Stream     *FileStream  `json:"stream,omitempty"`

	reader io.ReadCloser `json:"-"`
}

type FileStream struct {
	ID     string `json:"id"`
	Part   string `json:"part"`
	Index  int    `json:"index"`
	Count  int    `json:"count"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

type FileManagerCommandPayload struct {
	Action        string       `json:"action"`
	Path          string       `json:"path,omitempty"`
	Directory     string       `json:"directory,omitempty"`
	Name          string       `json:"name,omitempty"`
	EntryType     string       `json:"entryType,omitempty"`
	Content       string       `json:"content,omitempty"`
	IncludeHidden *bool        `json:"includeHidden,omitempty"`
	Encoding      FileEncoding `json:"encoding,omitempty"`
	Destination   string       `json:"destination,omitempty"`
	RequestID     string       `json:"requestId,omitempty"`
}

type Resource interface {
	isFileManagerResource()
}

func (DirectoryListing) isFileManagerResource() {}
func (FileContent) isFileManagerResource()      {}

func (f *FileContent) setStreamReader(r io.ReadCloser) {
	f.reader = r
}

func (f *FileContent) streamReader() io.ReadCloser {
	return f.reader
}
