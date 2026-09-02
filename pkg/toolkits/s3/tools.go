package s3

import (
	"context"
	"encoding/base64"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3client "github.com/txn2/mcp-s3/pkg/client"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"
)

// The two tools this toolkit registers (#1591). s3_list is buckets when no
// bucket is named and objects when one is; s3_object is one action over a
// (bucket, key), the shape manage_asset and manage_resource use.
const (
	toolList   = "s3_list"
	toolObject = "s3_object"

	listTitle   = "List S3 Buckets or Objects"
	objectTitle = "S3 Object"

	listDescription = "List the buckets of an S3 connection, or the objects in one bucket. " +
		"With no bucket: every bucket the connection can see (filtered by the connection's bucket_prefix when it sets one). " +
		"With a bucket: the objects under an optional prefix, grouped by an optional delimiter ('/' lists a folder level), " +
		"up to max_keys per page (default and maximum 1000), continued with the continuation_token the previous page returned. " +
		"Use this to browse a data lake, find the files in a bucket, or discover a key before reading it with s3_object."

	objectDescription = "Act on one S3 object by (bucket, key). action is one of: " +
		"get (download the object's content; text is returned as-is, binary as base64 with is_base64 true, bounded by the connection's max_get_size), " +
		"metadata (size, content type, last modified, ETag and custom metadata without the content), " +
		"put (upload a file to a bucket: content as text, or base64 with is_base64 true, with an optional content_type and metadata map), " +
		"copy (copy the object to dest_key in dest_bucket, defaulting to the same bucket), " +
		"delete (remove the object), " +
		"presign (get a download link or an upload link: a presigned URL for method GET or PUT that expires after expires_in seconds, default 3600, maximum 604800). " +
		"put, copy and delete are refused on a read-only connection, and the refusal names the connection."

	// The s3_object actions.
	actionGet      = "get"
	actionMetadata = "metadata"
	actionPut      = "put"
	actionCopy     = "copy"
	actionDelete   = "delete"
	actionPresign  = "presign"

	methodGet = "GET"
	methodPut = "PUT"

	defaultPresignSeconds = 3600
	maxPresignSeconds     = 604800
	maxListKeys           = 1000
)

var (
	destructive       = true
	listAnnotations   = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	objectAnnotations = &mcp.ToolAnnotations{DestructiveHint: &destructive}

	// The output schemas are the union of upstream's per-tool schemas for the
	// operations each tool performs. Upstream declares no required properties,
	// so a union is exactly the schema of "one of these results", and the
	// platform's output-schema middleware opens the top level as it does for
	// every advertised schema.
	listOutputSchema   = unionOutputSchema(s3tools.ToolListBuckets, s3tools.ToolListObjects)
	objectOutputSchema = unionOutputSchema(s3tools.ToolGetObject, s3tools.ToolGetObjectMetadata,
		s3tools.ToolPutObject, s3tools.ToolCopyObject, s3tools.ToolDeleteObject, s3tools.ToolPresignURL)
)

// unionOutputSchema merges the properties of upstream's default output schemas
// for names into one open object schema.
func unionOutputSchema(names ...s3tools.ToolName) map[string]any {
	props := map[string]any{}
	for _, name := range names {
		schema, ok := s3tools.DefaultOutputSchema(name).(map[string]any)
		if !ok {
			continue
		}
		if p, ok := schema["properties"].(map[string]any); ok {
			maps.Copy(props, p)
		}
	}
	return map[string]any{"type": "object", "properties": props}
}

// listInput is the s3_list argument set.
type listInput struct {
	Bucket            string `json:"bucket,omitempty" jsonschema:"bucket to list objects in; omit it to list the connection's buckets instead"`
	Prefix            string `json:"prefix,omitempty" jsonschema:"only objects whose key starts with this prefix"`
	Delimiter         string `json:"delimiter,omitempty" jsonschema:"groups keys at this character, commonly '/' to list one folder level; grouped prefixes come back as common_prefixes"`
	MaxKeys           int32  `json:"max_keys,omitempty" jsonschema:"objects per page, 1-1000 (default 1000)"`
	ContinuationToken string `json:"continuation_token,omitempty" jsonschema:"the next_continuation_token of the previous page, to continue listing"`
	Connection        string `json:"connection,omitempty" jsonschema:"S3 connection to use; the default connection when omitted"`
}

// objectInput is the s3_object argument set. Which fields matter depends on
// the action; the description says which.
type objectInput struct {
	Action      string            `json:"action" jsonschema:"one of get, metadata, put, copy, delete, presign"`
	Bucket      string            `json:"bucket" jsonschema:"bucket holding the object"`
	Key         string            `json:"key" jsonschema:"key (path) of the object"`
	Connection  string            `json:"connection,omitempty" jsonschema:"S3 connection to use; the default connection when omitted"`
	Content     string            `json:"content,omitempty" jsonschema:"put: the content to upload, as text or as base64 when is_base64 is true"`
	ContentType string            `json:"content_type,omitempty" jsonschema:"put: MIME type of the content (default application/octet-stream)"`
	IsBase64    bool              `json:"is_base64,omitempty" jsonschema:"put: content is base64-encoded binary"`
	Metadata    map[string]string `json:"metadata,omitempty" jsonschema:"put or copy: custom metadata to attach; on copy it replaces the source's"`
	DestBucket  string            `json:"dest_bucket,omitempty" jsonschema:"copy: destination bucket (default: the same bucket)"`
	DestKey     string            `json:"dest_key,omitempty" jsonschema:"copy: destination key"`
	Method      string            `json:"method,omitempty" jsonschema:"presign: GET for a download link (default) or PUT for an upload link"`
	ExpiresIn   int               `json:"expires_in,omitempty" jsonschema:"presign: seconds until the URL expires (default 3600, maximum 604800)"`
}

// handleList is s3_list: buckets when no bucket is named, objects when one is.
func (t *Toolkit) handleList(ctx context.Context, _ *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, any, error) {
	start := time.Now()
	op, res, out := t.list(ctx, in)
	t.observe(ctx, op, start, res)
	return res, out, nil
}

func (t *Toolkit) list(ctx context.Context, in listInput) (op string, res *mcp.CallToolResult, out any) {
	if in.Bucket == "" {
		op = toolList + ".buckets"
	} else {
		op = toolList + ".objects"
	}
	client, err := t.s3Toolkit.GetClient(in.Connection)
	if err != nil {
		return op, s3tools.ErrorResult(err.Error()), nil
	}
	if in.Bucket == "" {
		res, out = listBuckets(ctx, client, t.settings(in.Connection).bucketPrefix)
		return op, res, out
	}
	res, out = listObjects(ctx, client, in)
	return op, res, out
}

func listBuckets(ctx context.Context, client s3tools.S3Client, prefix string) (res *mcp.CallToolResult, out any) {
	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		return s3tools.ErrorResultf("failed to list buckets: %v", err), nil
	}
	result := s3tools.ListBucketsResult{Buckets: make([]s3tools.BucketResult, 0, len(buckets))}
	for _, b := range buckets {
		if prefix != "" && !strings.HasPrefix(b.Name, prefix) {
			continue
		}
		result.Buckets = append(result.Buckets, s3tools.BucketResult{Name: b.Name, CreationDate: stamp(b.CreationDate)})
	}
	result.Count = len(result.Buckets)
	return nil, &result
}

func listObjects(ctx context.Context, client s3tools.S3Client, in listInput) (res *mcp.CallToolResult, out any) {
	maxKeys := in.MaxKeys
	if maxKeys <= 0 || maxKeys > maxListKeys {
		maxKeys = maxListKeys
	}
	output, err := client.ListObjects(ctx, in.Bucket, in.Prefix, in.Delimiter, maxKeys, in.ContinuationToken)
	if err != nil {
		return s3tools.ErrorResultf("failed to list objects: %v", err), nil
	}
	result := s3tools.ListObjectsResult{
		Bucket:            in.Bucket,
		Prefix:            in.Prefix,
		Delimiter:         in.Delimiter,
		Objects:           make([]s3tools.ObjectResult, 0, len(output.Objects)),
		CommonPrefixes:    output.CommonPrefixes,
		Count:             len(output.Objects),
		IsTruncated:       output.IsTruncated,
		NextContinueToken: output.NextContinueToken,
	}
	for _, obj := range output.Objects {
		result.Objects = append(result.Objects, s3tools.ObjectResult{
			Key: obj.Key, Size: obj.Size, ETag: obj.ETag, StorageClass: obj.StorageClass, LastModified: stamp(obj.LastModified),
		})
	}
	return nil, &result
}

// handleObject is s3_object: one action over a (bucket, key).
func (t *Toolkit) handleObject(ctx context.Context, _ *mcp.CallToolRequest, in objectInput) (*mcp.CallToolResult, any, error) {
	start := time.Now()
	res, out := t.object(ctx, in)
	t.observe(ctx, toolObject+"."+operationLabel(in.Action), start, res)
	return res, out, nil
}

// operationLabel keeps the metric label to the known actions: the label is a
// series, and a caller-supplied string must not be able to mint series.
func operationLabel(action string) string {
	switch action {
	case actionGet, actionMetadata, actionPut, actionCopy, actionDelete, actionPresign:
		return action
	default:
		return "unknown"
	}
}

func (t *Toolkit) object(ctx context.Context, in objectInput) (res *mcp.CallToolResult, out any) {
	client, settings, refused := t.admitObject(in)
	if refused != nil {
		return refused, nil
	}
	return dispatchObject(ctx, client, settings, in)
}

// admitObject resolves the connection a call names and refuses what cannot
// proceed: a missing bucket or key, an unknown connection, or a writing action
// on a read-only connection.
func (t *Toolkit) admitObject(in objectInput) (s3tools.S3Client, connSettings, *mcp.CallToolResult) {
	if in.Bucket == "" {
		return nil, connSettings{}, s3tools.ErrorResult("bucket parameter is required")
	}
	if in.Key == "" {
		return nil, connSettings{}, s3tools.ErrorResult("key parameter is required")
	}
	client, err := t.s3Toolkit.GetClient(in.Connection)
	if err != nil {
		return nil, connSettings{}, s3tools.ErrorResult(err.Error())
	}
	settings := t.settings(in.Connection)
	if isWriteAction(in.Action) && settings.readOnly {
		return nil, connSettings{}, t.refuseWrite(in)
	}
	return client, settings, nil
}

func dispatchObject(ctx context.Context, client s3tools.S3Client, settings connSettings, in objectInput) (res *mcp.CallToolResult, out any) {
	switch in.Action {
	case actionGet:
		return getObject(ctx, client, in, settings.maxGetSize)
	case actionMetadata:
		return getMetadata(ctx, client, in)
	case actionPut:
		return putObject(ctx, client, in, settings.maxPutSize)
	case actionCopy:
		return copyObject(ctx, client, in)
	case actionDelete:
		return deleteObject(ctx, client, in)
	case actionPresign:
		return presignObject(ctx, client, in)
	default:
		return s3tools.ErrorResultf("unknown action %q: use get, metadata, put, copy, delete or presign", in.Action), nil
	}
}

func isWriteAction(action string) bool {
	return action == actionPut || action == actionCopy || action == actionDelete
}

// refuseWrite is the read-only refusal: it names the connection the write was
// addressed to, so a caller holding several knows which one declined.
func (t *Toolkit) refuseWrite(in objectInput) *mcp.CallToolResult {
	name := in.Connection
	if name == "" {
		name = t.name
	}
	return s3tools.ErrorResultf("connection %q is read-only: s3_object action %q is not permitted on it", name, in.Action)
}

func getObject(ctx context.Context, client s3tools.S3Client, in objectInput, maxGet int64) (res *mcp.CallToolResult, out any) {
	if maxGet > 0 {
		meta, err := client.GetObjectMetadata(ctx, in.Bucket, in.Key)
		if err != nil {
			return s3tools.ErrorResultf("failed to get object metadata: %v", err), nil
		}
		if meta.Size > maxGet {
			return s3tools.ErrorResultf("%v: object size %d bytes exceeds limit of %d bytes", s3tools.ErrSizeLimitExceeded, meta.Size, maxGet), nil
		}
	}
	content, err := client.GetObject(ctx, in.Bucket, in.Key)
	if err != nil {
		return s3tools.ErrorResultf("failed to get object: %v", err), nil
	}
	result := s3tools.GetObjectResult{
		Bucket: in.Bucket, Key: in.Key, Size: content.Size, ContentType: content.ContentType,
		ETag: content.ETag, Metadata: content.Metadata, LastModified: stamp(content.LastModified),
	}
	if isTextContent(content.ContentType, content.Body) {
		result.Content = string(content.Body)
	} else {
		result.Content, result.IsBase64 = base64.StdEncoding.EncodeToString(content.Body), true
	}
	return nil, &result
}

func getMetadata(ctx context.Context, client s3tools.S3Client, in objectInput) (res *mcp.CallToolResult, out any) {
	meta, err := client.GetObjectMetadata(ctx, in.Bucket, in.Key)
	if err != nil {
		return s3tools.ErrorResultf("failed to get object metadata: %v", err), nil
	}
	return nil, &s3tools.GetObjectMetadataResult{
		Bucket: in.Bucket, Key: in.Key, Size: meta.Size, ContentType: meta.ContentType, ContentLength: meta.ContentLength,
		ETag: meta.ETag, Metadata: meta.Metadata, LastModified: stamp(meta.LastModified),
	}
}

func putObject(ctx context.Context, client s3tools.S3Client, in objectInput, maxPut int64) (res *mcp.CallToolResult, out any) {
	if in.Content == "" {
		return s3tools.ErrorResult("content parameter is required"), nil
	}
	body := []byte(in.Content)
	if in.IsBase64 {
		decoded, err := base64.StdEncoding.DecodeString(in.Content)
		if err != nil {
			return s3tools.ErrorResultf("failed to decode base64 content: %v", err), nil
		}
		body = decoded
	}
	if maxPut > 0 && int64(len(body)) > maxPut {
		return s3tools.ErrorResultf("%v: content size %d bytes exceeds limit of %d bytes", s3tools.ErrSizeLimitExceeded, len(body), maxPut), nil
	}
	contentType := in.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	output, err := client.PutObject(ctx, &s3client.PutObjectInput{
		Bucket: in.Bucket, Key: in.Key, Body: body, ContentType: contentType, Metadata: in.Metadata,
	})
	if err != nil {
		return s3tools.ErrorResultf("failed to put object: %v", err), nil
	}
	return nil, &s3tools.PutObjectResult{Bucket: in.Bucket, Key: in.Key, Size: int64(len(body)), ETag: output.ETag, VersionID: output.VersionID}
}

func copyObject(ctx context.Context, client s3tools.S3Client, in objectInput) (res *mcp.CallToolResult, out any) {
	if in.DestKey == "" {
		return s3tools.ErrorResult("dest_key parameter is required for copy"), nil
	}
	destBucket := in.DestBucket
	if destBucket == "" {
		destBucket = in.Bucket
	}
	output, err := client.CopyObject(ctx, &s3client.CopyObjectInput{
		SourceBucket: in.Bucket, SourceKey: in.Key, DestBucket: destBucket, DestKey: in.DestKey, Metadata: in.Metadata,
	})
	if err != nil {
		return s3tools.ErrorResultf("failed to copy object: %v", err), nil
	}
	return nil, &s3tools.CopyObjectResult{
		SourceBucket: in.Bucket, SourceKey: in.Key, DestBucket: destBucket, DestKey: in.DestKey,
		ETag: output.ETag, VersionID: output.VersionID, LastModified: stamp(output.LastModified),
	}
}

func deleteObject(ctx context.Context, client s3tools.S3Client, in objectInput) (res *mcp.CallToolResult, out any) {
	if err := client.DeleteObject(ctx, in.Bucket, in.Key); err != nil {
		return s3tools.ErrorResultf("failed to delete object: %v", err), nil
	}
	return nil, &s3tools.DeleteObjectResult{Bucket: in.Bucket, Key: in.Key, Deleted: true}
}

func presignObject(ctx context.Context, client s3tools.S3Client, in objectInput) (res *mcp.CallToolResult, out any) {
	method := strings.ToUpper(in.Method)
	if method == "" {
		method = methodGet
	}
	if method != methodGet && method != methodPut {
		return s3tools.ErrorResultf("invalid method: %s (must be GET or PUT)", in.Method), nil
	}
	expiresIn := in.ExpiresIn
	if expiresIn < 1 {
		expiresIn = defaultPresignSeconds
	}
	if expiresIn > maxPresignSeconds {
		expiresIn = maxPresignSeconds
	}
	expires := time.Duration(expiresIn) * time.Second
	var (
		url *s3client.PresignedURL
		err error
	)
	if method == methodGet {
		url, err = client.PresignGetURL(ctx, in.Bucket, in.Key, expires)
	} else {
		url, err = client.PresignPutURL(ctx, in.Bucket, in.Key, expires)
	}
	if err != nil {
		return s3tools.ErrorResultf("failed to generate presigned %s URL: %v", method, err), nil
	}
	return nil, &s3tools.PresignURLResult{
		Bucket: in.Bucket, Key: in.Key, URL: url.URL, Method: url.Method, ExpiresIn: expiresIn, ExpiresAt: stamp(url.ExpiresAt),
	}
}

// stamp renders a timestamp the way every S3 result does, and nothing for the
// zero time.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// isTextContent decides whether a get returns the bytes as text or as base64:
// a textual declared type, or bytes that are valid UTF-8 with no NUL.
func isTextContent(contentType string, body []byte) bool {
	contentType = strings.ToLower(contentType)
	for _, prefix := range []string{
		"text/", "application/json", "application/xml", "application/javascript",
		"application/x-yaml", "application/yaml", "application/toml", "application/x-sh",
	} {
		if strings.Contains(contentType, prefix) {
			return true
		}
	}
	return !slices.Contains(body, 0) && utf8.Valid(body)
}
