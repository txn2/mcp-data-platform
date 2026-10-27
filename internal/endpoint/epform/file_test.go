package epform

import (
	"bytes"
	"mime/multipart"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pngBytes is the start of a PNG file, which is what its type is judged by.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 32)...)

func photos() Spec {
	s := Spec{Fields: []Field{
		{Name: "name", Type: TypeText, Required: true},
		{Name: "photos", Type: TypeFile, Required: true, MaxFiles: 2, MaxBytes: 100, Accept: []string{"image/*"}},
		{Name: "brief", Type: TypeFile, Accept: []string{"application/pdf"}},
	}}
	s.normalize()
	return s
}

// multipartBody is a multipart submission of values and files.
func multipartBody(t *testing.T, values map[string]string, files []Upload) (contentType string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range values {
		require.NoError(t, w.WriteField(k, v))
	}
	for _, f := range files {
		part, err := w.CreateFormFile(f.Field, f.Name)
		require.NoError(t, err)
		_, err = part.Write(f.Data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return w.FormDataContentType(), buf.Bytes()
}

// #2333: a multipart submission reads its values and its files, each file
// under the field it was sent in.
func TestRead_Multipart(t *testing.T) {
	ct, body := multipartBody(t, map[string]string{"name": "Ann"}, []Upload{
		{Field: "photos", Name: "a.png", Data: pngBytes}, {Field: "photos", Name: "b.png", Data: pngBytes},
	})
	values, files, err := Read(ct, body)
	require.NoError(t, err)
	assert.Equal(t, []string{"Ann"}, values["name"])
	require.Len(t, files, 2)
	assert.Equal(t, Upload{Field: "photos", Name: "a.png", Data: pngBytes}, files[0])
	assert.Equal(t, "image/png", files[0].ContentType())

	_, _, err = Read("multipart/form-data", body)
	require.ErrorContains(t, err, "names no boundary")
	_, _, err = Read("multipart/form-data; boundary=nope", []byte("--nope\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\nno closing boundary"))
	require.ErrorContains(t, err, "not a multipart form")
}

func TestRead_MultipartPartBound(t *testing.T) {
	values := map[string]string{}
	for i := range maxParts + 1 {
		values[string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))] = "x"
	}
	ct, body := multipartBody(t, values, nil)
	_, _, err := Read(ct, body)
	require.ErrorContains(t, err, "more than 1000 parts")
}

// #2333: each file is described as form_submit is handed it, its type judged
// from its bytes; past max_bytes, a type the field does not take and one file
// more than max_files are each refused on the field.
func TestCheck_Files(t *testing.T) {
	issued := time.Unix(1_800_000_000, 0)
	later := issued.Add(time.Minute)
	spec := photos()
	pdfNamedPNG := Upload{Field: "photos", Name: "c.png", Data: []byte("%PDF-1.7\n%")}

	values, errs, err := Check(spec, issued, map[string][]string{"name": {"Ann"}},
		[]Upload{{Field: "photos", Name: "a.png", Data: pngBytes}, {Field: "photos", Name: "b.png", Data: pngBytes}}, later)
	require.NoError(t, err)
	require.Empty(t, errs)
	size := int64(len(pngBytes))
	assert.Equal(t, []any{
		map[string]any{FileKeyName: "a.png", FileKeyContentType: "image/png", FileKeySize: size},
		map[string]any{FileKeyName: "b.png", FileKeyContentType: "image/png", FileKeySize: size},
	}, values["photos"])
	assert.Equal(t, []any{}, values["brief"], "an optional file field sent no files is an empty list")

	refusedOn := func(t *testing.T, field, want string, files ...Upload) {
		t.Helper()
		_, errs, err := Check(spec, issued, map[string][]string{"name": {"Ann"}}, files, later)
		require.NoError(t, err)
		assert.Contains(t, errs[field], want)
	}
	big := Upload{Field: "photos", Name: "big.png", Data: append(append([]byte{}, pngBytes...), make([]byte, 100)...)}
	refusedOn(t, "photos", "larger than 100 bytes", big)
	refusedOn(t, "photos", "is application/pdf, which this field does not take", pdfNamedPNG)
	three := Upload{Field: "photos", Name: "x.png", Data: pngBytes}
	refusedOn(t, "photos", "at most 2 files", three, three, three)
	refusedOn(t, "photos", "required")
	refusedOn(t, "elsewhere", "no such field", Upload{Field: "elsewhere", Name: "a.png", Data: pngBytes}, three)
	refusedOn(t, "name", "takes no files", Upload{Field: "name", Name: "a.png", Data: pngBytes}, three)

	_, errs, _ = Check(spec, issued, map[string][]string{"name": {"Ann"}, "photos": {"a.png"}}, []Upload{three}, later)
	assert.Equal(t, "this field takes files", errs["photos"])

	one := Spec{Fields: []Field{{Name: "cv", Type: TypeFile}}}
	one.normalize()
	_, errs, _ = Check(one, issued, nil, []Upload{{Field: "cv", Data: pngBytes}, {Field: "cv", Data: pngBytes}}, later)
	assert.Equal(t, "this field takes at most 1 file", errs["cv"])
	_, errs, _ = Check(one, issued, nil, []Upload{{Field: "cv", Data: append(bytes.Repeat([]byte("a"), int(DefaultMaxFileBytes)), 'a')}}, later)
	assert.Contains(t, errs["cv"], "a file is larger than")
}

// #2333: what a file field may declare, and that nothing else declares it.
func TestValidate_FileFields(t *testing.T) {
	spec := photos()
	require.NoError(t, spec.Validate())
	assert.True(t, spec.HasFiles())
	assert.False(t, contact().HasFiles())
	assert.Equal(t, DefaultMaxFiles, spec.Fields[2].MaxFiles)
	assert.Equal(t, DefaultMaxFileBytes, spec.Fields[2].MaxBytes)

	require.NoError(t, spec.CheckUploads(0), "no ceiling")
	require.NoError(t, spec.CheckUploads(DefaultMaxFileBytes))
	require.ErrorContains(t, spec.CheckUploads(50), `file field "photos" has max_bytes 100, past this deployment's upload limit of 50 bytes`)

	for name, tc := range map[string]struct {
		field Field
		want  string
	}{
		"too many files":   {Field{Name: "f", Type: TypeFile, MaxLength: 1, MaxFiles: MaxFiles + 1, MaxBytes: 1}, "max_files 21"},
		"no bytes":         {Field{Name: "f", Type: TypeFile, MaxLength: 1, MaxFiles: 1, MaxBytes: -1}, "max_bytes -1"},
		"sensitive":        {Field{Name: "f", Type: TypeFile, MaxLength: 1, MaxFiles: 1, MaxBytes: 1, Sensitive: true}, "is sensitive"},
		"bad accept":       {Field{Name: "f", Type: TypeFile, MaxLength: 1, MaxFiles: 1, MaxBytes: 1, Accept: []string{"png"}}, `accept "png"`},
		"wildcard type":    {Field{Name: "f", Type: TypeFile, MaxLength: 1, MaxFiles: 1, MaxBytes: 1, Accept: []string{"*/*"}}, `accept "*/*"`},
		"params":           {Field{Name: "f", Type: TypeFile, MaxLength: 1, MaxFiles: 1, MaxBytes: 1, Accept: []string{"text/plain; charset=utf-8"}}, "accept"},
		"too many accepts": {Field{Name: "f", Type: TypeFile, MaxLength: 1, MaxFiles: 1, MaxBytes: 1, Accept: append(make([]string, maxAccept), "image/png")}, "more than 50 types"},
		"text with bounds": {Field{Name: "f", Type: TypeText, MaxLength: 10, MaxFiles: 2}, "a file field's"},
	} {
		t.Run(name, func(t *testing.T) {
			s := Spec{Fields: []Field{tc.field}}
			if tc.field.Type == TypeText {
				s.normalize()
			}
			require.ErrorContains(t, s.Validate(), tc.want)
		})
	}

	prefilled := photos()
	prefilled.Prefill = map[string]any{"photos": "x"}
	require.ErrorContains(t, prefilled.Validate(), "a file field starts empty")
}

func TestAccepts(t *testing.T) {
	assert.True(t, Field{Type: TypeFile}.accepts("application/zip"), "no accept list takes any type")
	exact := Field{Type: TypeFile, Accept: []string{"application/pdf", "image/*"}}
	assert.True(t, exact.accepts("application/pdf"))
	assert.True(t, exact.accepts("image/jpeg"))
	assert.False(t, exact.accepts("imagex/jpeg"))
	assert.False(t, exact.accepts("text/plain"))
}
