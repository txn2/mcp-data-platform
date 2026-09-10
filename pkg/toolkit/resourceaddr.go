package toolkit

// ResourceAddress is where a managed resource is filed: the library, the folder
// chain inside it, and the file's name in that folder (#1665).
//
// It is the whole of a resource's address and the thing its canonical mcp://
// URI is built from, which is what makes it an identity a caller can hold
// without holding an id. A scheduled script's memory of its own output is
// exactly the thing that gets cleared and rewritten, so the address is what
// survives across runs where a remembered id does not.
//
// It lives here beside ResourceDestination because the two are the same address
// arriving for different reasons -- one to write into, one to look up -- and a
// second definition of "where a file is" is how the two would drift.
type ResourceAddress struct {
	// Scope and ScopeID name the library. An empty scope means the caller's
	// own, which is the one library every authenticated caller may write.
	Scope   string
	ScopeID string
	// Path is the folder chain inside the library, for example
	// "datasets/media-manager".
	Path string
	// Filename is the file's name within that folder, and the last segment of
	// the canonical URI.
	Filename string
}

// ResourceQuery narrows a listing of managed resources to one library and one
// folder.
type ResourceQuery struct {
	// Scope and ScopeID name the library. An empty scope is every library the
	// caller can see, which is deliberately wider than a write's default: a
	// listing answers "what is filed here", and someone browsing has no reason
	// to be shown only their own library when they can see three.
	Scope   string
	ScopeID string
	// Path is the folder the listing is rooted at, and it is a prefix: the
	// answer holds what is beneath it at every depth, which is what makes a
	// folder count mean anything. Empty is the whole library.
	Path string
	// Limit and Offset page the answer. A non-positive limit takes the
	// implementation's default.
	Limit  int
	Offset int
}
