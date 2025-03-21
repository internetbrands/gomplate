package datafs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/hairyhenderson/go-fsimpl"
	"github.com/hairyhenderson/gomplate/v4/internal/config"
	"github.com/hairyhenderson/gomplate/v4/internal/iohelpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseURL(in string) *url.URL {
	u, _ := url.Parse(in)
	return u
}

func setupMergeFsys(ctx context.Context, t *testing.T) fs.FS {
	t.Helper()

	jsonContent := `{"hello": "world"}`
	yamlContent := "hello: earth\ngoodnight: moon\n"
	arrayContent := `["hello", "world"]`

	wd := wdForTest(t)

	fsys := WrapWdFS(fstest.MapFS{
		"tmp":                              {Mode: fs.ModeDir | 0o777},
		"tmp/jsonfile.json":                {Data: []byte(jsonContent)},
		"tmp/array.json":                   {Data: []byte(arrayContent)},
		"tmp/yamlfile.yaml":                {Data: []byte(yamlContent)},
		"tmp/textfile.txt":                 {Data: []byte(`plain text...`)},
		path.Join(wd, "jsonfile.json"):     {Data: []byte(jsonContent)},
		path.Join(wd, "array.json"):        {Data: []byte(arrayContent)},
		path.Join(wd, "yamlfile.yaml"):     {Data: []byte(yamlContent)},
		path.Join(wd, "textfile.txt"):      {Data: []byte(`plain text...`)},
		path.Join(wd, "tmp/jsonfile.json"): {Data: []byte(jsonContent)},
		path.Join(wd, "tmp/array.json"):    {Data: []byte(arrayContent)},
		path.Join(wd, "tmp/yamlfile.yaml"): {Data: []byte(yamlContent)},
		path.Join(wd, "tmp/textfile.txt"):  {Data: []byte(`plain text...`)},
	})

	reg := NewRegistry()
	reg.Register("foo", config.DataSource{
		URL: mustParseURL("merge:file:///tmp/jsonfile.json|file:///tmp/yamlfile.yaml"),
	})
	reg.Register("bar", config.DataSource{URL: mustParseURL("file:///tmp/jsonfile.json")})
	reg.Register("baz", config.DataSource{URL: mustParseURL("file:///tmp/yamlfile.yaml")})
	reg.Register("text", config.DataSource{URL: mustParseURL("file:///tmp/textfile.txt")})
	reg.Register("badscheme", config.DataSource{URL: mustParseURL("bad:///scheme.json")})
	// mime type overridden by URL query, should fail to parse
	reg.Register("badtype", config.DataSource{URL: mustParseURL("file:///tmp/textfile.txt?type=foo/bar")})
	reg.Register("array", config.DataSource{
		URL: mustParseURL("file:///tmp/array.json?type=" + url.QueryEscape(iohelpers.JSONArrayMimetype)),
	})

	mux := fsimpl.NewMux()
	mux.Add(MergeFS)
	mux.Add(WrappedFSProvider(fsys, "file", ""))

	ctx = ContextWithFSProvider(ctx, mux)

	fsys, err := NewMergeFS(mustParseURL("merge:///"))
	require.NoError(t, err)

	fsys = WithDataSourceRegistryFS(reg, fsys)
	fsys = fsimpl.WithContextFS(ctx, fsys)

	return fsys
}

func wdForTest(t *testing.T) string {
	t.Helper()

	wd, _ := os.Getwd()

	// MapFS doesn't support windows path separators, so we use / exclusively
	vol := filepath.VolumeName(wd)
	if vol != "" && wd != vol {
		wd = wd[len(vol)+1:]
	} else if wd[0] == '/' {
		wd = wd[1:]
	}
	wd = filepath.ToSlash(wd)

	return wd
}

func TestMergeData(t *testing.T) {
	def := map[string]interface{}{
		"f": true,
		"t": false,
		"z": "def",
	}
	out, err := mergeData([]map[string]interface{}{def})
	require.NoError(t, err)
	assert.Equal(t, "f: true\nt: false\nz: def\n", string(out))

	over := map[string]interface{}{
		"f": false,
		"t": true,
		"z": "over",
	}
	out, err = mergeData([]map[string]interface{}{over, def})
	require.NoError(t, err)
	assert.Equal(t, "f: false\nt: true\nz: over\n", string(out))

	over = map[string]interface{}{
		"f": false,
		"t": true,
		"z": "over",
		"m": map[string]interface{}{
			"a": "aaa",
		},
	}
	out, err = mergeData([]map[string]interface{}{over, def})
	require.NoError(t, err)
	assert.Equal(t, "f: false\nm:\n  a: aaa\nt: true\nz: over\n", string(out))

	uber := map[string]interface{}{
		"z": "über",
	}
	out, err = mergeData([]map[string]interface{}{uber, over, def})
	require.NoError(t, err)
	assert.Equal(t, "f: false\nm:\n  a: aaa\nt: true\nz: über\n", string(out))

	uber = map[string]interface{}{
		"m": "notamap",
		"z": map[string]interface{}{
			"b": "bbb",
		},
	}
	out, err = mergeData([]map[string]interface{}{uber, over, def})
	require.NoError(t, err)
	assert.Equal(t, "f: false\nm: notamap\nt: true\nz:\n  b: bbb\n", string(out))

	uber = map[string]interface{}{
		"m": map[string]interface{}{
			"b": "bbb",
		},
	}
	out, err = mergeData([]map[string]interface{}{uber, over, def})
	require.NoError(t, err)
	assert.Equal(t, "f: false\nm:\n  a: aaa\n  b: bbb\nt: true\nz: over\n", string(out))
}

func TestMergeFS_Open(t *testing.T) {
	fsys := setupMergeFsys(context.Background(), t)
	assert.IsType(t, &mergeFS{}, fsys)

	_, err := fsys.Open("/")
	require.Error(t, err)

	_, err = fsys.Open("just/one/part")
	require.Error(t, err)
	require.ErrorContains(t, err, "need at least 2 datasources to merge")

	// missing aliases, fallback to relative files, but there's no FS registered
	// for the empty scheme
	_, err = fsys.Open("a|b")
	require.ErrorIs(t, err, fs.ErrNotExist)

	// missing alias
	_, err = fsys.Open("bogusalias|file:///tmp/jsonfile.json")
	require.ErrorIs(t, err, fs.ErrNotExist)

	// unregistered scheme
	_, err = fsys.Open("file:///tmp/jsonfile.json|badscheme")
	require.ErrorContains(t, err, "no filesystem registered for scheme \"bad\"")
}

func TestMergeFile_Read(t *testing.T) {
	fsys := fstest.MapFS{
		"one.yml":    {Data: []byte("one: 1\n")},
		"two.json":   {Data: []byte(`{"one": false, "two": 2}`)},
		"three.toml": {Data: []byte("one = 999\nthree = 3\n")},
	}

	files := make([]subFile, 3)
	for i, fn := range []string{"one.yml", "two.json", "three.toml"} {
		f, _ := fsys.Open(fn)
		defer f.Close()

		ct := mime.TypeByExtension(filepath.Ext(fn))

		files[i] = subFile{f, ct}
	}

	mf := &mergeFile{name: "one.yml|two.json|three.toml", subFiles: files}

	b, err := io.ReadAll(mf)
	require.NoError(t, err)
	assert.Equal(t, "one: 1\nthree: 3\ntwo: 2\n", string(b))

	// now try with partial reads
	for i, fn := range []string{"one.yml", "two.json", "three.toml"} {
		f, _ := fsys.Open(fn)
		defer f.Close()

		ct := mime.TypeByExtension(filepath.Ext(fn))

		files[i] = subFile{f, ct}
	}

	mf = &mergeFile{name: "one.yml|two.json|three.toml", subFiles: files}

	p := make([]byte, 10)
	n, err := mf.Read(p)
	require.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, "one: 1\nthr", string(p))

	n, err = mf.Read(p)
	require.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, "ee: 3\ntwo:", string(p))

	n, err = mf.Read(p)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, " 2\n 3\ntwo:", string(p))
}

func TestMergeFS_ReadFile(t *testing.T) {
	mergedContent := "goodnight: moon\nhello: world\n"

	fsys := setupMergeFsys(context.Background(), t)

	testdata := []string{
		// absolute URLs
		"file:///tmp/jsonfile.json|file:///tmp/yamlfile.yaml",
		// aliases
		"bar|baz",
		// mixed relative file and alias
		"jsonfile.json|baz",
		// relative file with ./ and alias
		"./jsonfile.json|baz",
	}

	for _, td := range testdata {
		t.Run(td, func(t *testing.T) {
			f, err := fsys.Open(td)
			require.NoError(t, err)
			defer f.Close()

			b, err := io.ReadAll(f)
			require.NoError(t, err)
			assert.Equal(t, mergedContent, string(b))
		})
	}

	// read errors
	errortests := []struct {
		in            string
		expectedError string
	}{
		{"file:///tmp/jsonfile.json|badtype", "data of type \"foo/bar\" not yet supported"},
		{"file:///tmp/jsonfile.json|array", "can only merge maps"},
	}

	for _, td := range errortests {
		t.Run(td.in, func(t *testing.T) {
			f, err := fsys.Open(td.in)
			require.NoError(t, err)
			defer f.Close()

			_, err = io.ReadAll(f)
			require.Error(t, err)
			assert.Contains(t, err.Error(), td.expectedError)
		})
	}
}

func TestMergeFS_ReadsSubFilesOnce(t *testing.T) {
	mergedContent := "goodnight: moon\nhello: world\n"

	wd := wdForTest(t)

	fsys := WrapWdFS(
		openOnce(&fstest.MapFS{
			path.Join(wd, "tmp/jsonfile.json"): {Data: []byte(`{"hello": "world"}`)},
			path.Join(wd, "tmp/yamlfile.yaml"): {Data: []byte("hello: earth\ngoodnight: moon\n")},
		}))

	mux := fsimpl.NewMux()
	mux.Add(MergeFS)
	mux.Add(WrappedFSProvider(fsys, "file", ""))

	ctx := ContextWithFSProvider(context.Background(), mux)

	reg := NewRegistry()
	reg.Register("jsonfile", config.DataSource{URL: mustParseURL("tmp/jsonfile.json")})
	reg.Register("yamlfile", config.DataSource{URL: mustParseURL("tmp/yamlfile.yaml")})

	fsys, err := NewMergeFS(mustParseURL("merge:///"))
	require.NoError(t, err)

	fsys = WithDataSourceRegistryFS(reg, fsys)
	fsys = fsimpl.WithContextFS(ctx, fsys)

	b, err := fs.ReadFile(fsys, "jsonfile|yamlfile")
	require.NoError(t, err)
	assert.Equal(t, mergedContent, string(b))
}

type openOnceFS struct {
	fs     *fstest.MapFS
	opened map[string]struct{}
}

// a filesystem that only allows opening or stating a file once
func openOnce(fsys *fstest.MapFS) fs.FS {
	return &openOnceFS{fs: fsys, opened: map[string]struct{}{}}
}

func (f *openOnceFS) Open(name string) (fs.File, error) {
	if _, ok := f.opened[name]; ok {
		return nil, fmt.Errorf("open: %q already opened", name)
	}

	f.opened[name] = struct{}{}

	return f.fs.Open(name)
}
