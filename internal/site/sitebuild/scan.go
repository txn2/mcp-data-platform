package sitebuild

import "strings"

// envRoot is import.meta.env read whole rather than one value of it.
const envRoot = "import.meta.env"

const siteMeta = "import.meta.site"

// metaRead is one read of import.meta.env a script makes.
type metaRead struct {
	key          string
	line, column int
}

// scanMeta reads a script lexically for its reads of import.meta.env, which a
// workstation bundler fills in and the platform's compile fills in only from
// define (#2781), and reports whether it reads import.meta.site. Comments and
// string text are skipped; the expressions inside a template literal are not.
func scanMeta(body []byte) (reads []metaRead, site bool) {
	s := &metaScanner{src: string(body), line: 1, lineStart: 0}
	for s.pos < len(s.src) {
		s.step(&reads, &site)
	}
	return reads, site
}

type metaScanner struct {
	src       string
	pos       int
	line      int
	lineStart int
	// templates holds, for each template literal whose ${ } the scanner is
	// inside, the brace depth at which that expression closes.
	templates []int
	depth     int
}

func (s *metaScanner) newline(at int) {
	s.line++
	s.lineStart = at + 1
}

func (s *metaScanner) step(reads *[]metaRead, site *bool) {
	if s.skipText() {
		return
	}
	switch c := s.src[s.pos]; {
	case c == '{':
		s.depth++
		s.pos++
	case c == '}':
		s.closeBrace()
	case strings.HasPrefix(s.src[s.pos:], "import.meta.") && s.boundary():
		s.readMeta(reads, site)
	default:
		s.pos++
	}
}

// skipText steps over a line end, a comment or a string, reporting whether it
// did.
func (s *metaScanner) skipText() bool {
	c := s.src[s.pos]
	switch {
	case c == '\n':
		s.newline(s.pos)
		s.pos++
	case strings.HasPrefix(s.src[s.pos:], "//"):
		s.skipTo("\n", false)
	case strings.HasPrefix(s.src[s.pos:], "/*"):
		s.skipTo("*/", true)
	case c == '\'' || c == '"':
		s.skipQuoted(c)
	case c == '`':
		s.pos++
		s.skipTemplate()
	default:
		return false
	}
	return true
}

func (s *metaScanner) closeBrace() {
	s.pos++
	if n := len(s.templates); n > 0 && s.templates[n-1] == s.depth {
		s.templates = s.templates[:n-1]
		s.skipTemplate()
		return
	}
	s.depth--
}

// boundary reports that import starts a name: nothing that continues one
// stands before it.
func (s *metaScanner) boundary() bool {
	if s.pos == 0 {
		return true
	}
	p := s.src[s.pos-1]
	return !isIdentRune(p) && p != '.'
}

func isIdentRune(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func (s *metaScanner) readMeta(reads *[]metaRead, site *bool) {
	line, col := s.line, s.pos-s.lineStart+1
	rest := s.src[s.pos:]
	switch {
	case strings.HasPrefix(rest, siteMeta) && !s.continues(len(siteMeta)):
		*site = true
		s.pos += len(siteMeta)
		return
	case !strings.HasPrefix(rest, envRoot) || s.continues(len(envRoot)):
		s.pos += len("import.meta.")
		return
	}
	s.pos += len(envRoot)
	key := envRoot
	if s.pos < len(s.src) && s.src[s.pos] == '.' {
		end := s.pos + 1
		for end < len(s.src) && isIdentRune(s.src[end]) {
			end++
		}
		if end > s.pos+1 {
			key = envRoot + s.src[s.pos:end]
			s.pos = end
		}
	}
	*reads = append(*reads, metaRead{key: key, line: line, column: col})
}

// continues reports a name that goes on past n bytes: import.meta.envelope is
// not import.meta.env.
func (s *metaScanner) continues(n int) bool {
	at := s.pos + n
	return at < len(s.src) && isIdentRune(s.src[at])
}

func (s *metaScanner) skipTo(end string, counting bool) {
	i := strings.Index(s.src[s.pos:], end)
	stop := len(s.src)
	if i >= 0 {
		stop = s.pos + i + len(end)
	}
	if counting {
		for j := s.pos; j < stop; j++ {
			if s.src[j] == '\n' {
				s.newline(j)
			}
		}
	}
	s.pos = stop
}

func (s *metaScanner) skipQuoted(q byte) {
	s.pos++
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case '\\':
			s.pos += 2
			continue
		case q, '\n':
			s.pos++
			return
		}
		s.pos++
	}
}

// skipTemplate skips template text up to its end or to a ${, whose expression
// the scanner then reads as code.
func (s *metaScanner) skipTemplate() {
	for s.pos < len(s.src) {
		switch {
		case s.src[s.pos] == '\\':
			s.pos += 2
		case s.src[s.pos] == '`':
			s.pos++
			return
		case strings.HasPrefix(s.src[s.pos:], "${"):
			s.pos += 2
			s.depth++
			s.templates = append(s.templates, s.depth)
			return
		default:
			if s.src[s.pos] == '\n' {
				s.newline(s.pos)
			}
			s.pos++
		}
	}
}
