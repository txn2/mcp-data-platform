// Package storage provides abstractions for storage providers.
package storage

import "time"

// DatasetIdentifier uniquely identifies a dataset in storage.
type DatasetIdentifier struct {
	Bucket     string `json:"bucket"`
	Prefix     string `json:"prefix,omitempty"`
	Connection string `json:"connection,omitempty"`
}

// String returns a string representation.
func (d DatasetIdentifier) String() string {
	if d.Prefix != "" {
		return d.Bucket + "/" + d.Prefix
	}
	return d.Bucket
}

// DatasetAvailability indicates if a dataset is available in storage.
type DatasetAvailability struct {
	Available   bool       `json:"available"`
	Bucket      string     `json:"bucket,omitempty"`
	Prefix      string     `json:"prefix,omitempty"`
	Connection  string     `json:"connection,omitempty"`
	ObjectCount int64      `json:"object_count,omitempty"`
	TotalSize   int64      `json:"total_size,omitempty"`
	LastUpdated *time.Time `json:"last_updated,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// ObjectInfo provides information about a storage object.
type ObjectInfo struct {
	Key          string            `json:"key"`
	Bucket       string            `json:"bucket"`
	Size         int64             `json:"size"`
	LastModified *time.Time        `json:"last_modified,omitempty"`
	ContentType  string            `json:"content_type,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// AccessExample provides an example of how to access a dataset.
type AccessExample struct {
	Description string `json:"description"`
	Command     string `json:"command"`
	SDK         string `json:"sdk,omitempty"`
}
