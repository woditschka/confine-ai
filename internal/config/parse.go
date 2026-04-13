package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// maxConfigFileSize is the maximum allowed configuration file size.
// Defense in depth against oversized input.
const maxConfigFileSize = 10 << 20 // 10 MB

// RawConfig holds the raw JSON content of a parsed devcontainer.json file
// after JSONC extensions have been stripped. The JSON field contains valid
// JSON that can be unmarshaled into typed structures.
type RawConfig struct {
	// Path is the absolute path to the source file.
	Path string

	// JSON is the clean JSON content after stripping comments and
	// trailing commas. It is valid JSON suitable for encoding/json.
	JSON json.RawMessage
}

// Parse reads a devcontainer.json file at the given path, strips JSONC
// extensions (comments and trailing commas), and returns the parsed
// configuration as a RawConfig.
//
// Parse returns an error if the file cannot be read or contains invalid
// JSON after JSONC stripping.
func Parse(path string) (RawConfig, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return RawConfig{}, fmt.Errorf("parse config: %q: %w", path, err)
	}

	f, err := os.Open(absPath)
	if err != nil {
		return RawConfig{}, fmt.Errorf("parse config: %q: %w", absPath, err)
	}
	defer f.Close() //nolint:errcheck // read-only file, close error is not actionable

	// Read up to maxConfigFileSize+1 bytes atomically from the open fd.
	// If we get more than maxConfigFileSize, the file is too large.
	data, err := io.ReadAll(io.LimitReader(f, maxConfigFileSize+1))
	if err != nil {
		return RawConfig{}, fmt.Errorf("parse config: %q: %w", absPath, err)
	}

	if len(data) > maxConfigFileSize {
		return RawConfig{}, fmt.Errorf("parse config: %q exceeds maximum size (%d bytes)", absPath, maxConfigFileSize)
	}

	clean, err := stripJSONC(data)
	if err != nil {
		return RawConfig{}, fmt.Errorf("parse config: %q: %w", absPath, err)
	}

	// Validate JSON and get a descriptive error on failure.
	var v json.RawMessage
	if err := json.Unmarshal(clean, &v); err != nil {
		return RawConfig{}, fmt.Errorf("parse config: %q: %w", absPath, err)
	}

	return RawConfig{
		Path: absPath,
		JSON: json.RawMessage(clean),
	}, nil
}

// scanState represents the current state of the JSONC scanner.
type scanState int

const (
	stateNormal       scanState = iota
	stateString                 // inside a double-quoted string
	stateLineComment            // inside a // comment
	stateBlockComment           // inside a /* comment
)

// jsoncScanner strips JSONC extensions from source bytes, producing valid JSON.
// The scanner is a finite-state machine with four states. Each state has a
// dedicated handler method, keeping transition logic focused and testable.
type jsoncScanner struct {
	src []byte // input bytes
	out []byte // output buffer
	pos int    // current read position in src

	state scanState

	// Trailing comma detection: pendingComma tracks whether we have a
	// buffered comma that might be trailing. pendingWS holds whitespace
	// seen after the comma (and after any stripped comments).
	pendingComma bool
	pendingWS    []byte

	// Comment-only line detection: lineHasContent tracks whether the
	// current output line (since the last \n) has any non-whitespace
	// content. commentOnlyLine is set when entering a line comment on
	// a line that has no non-whitespace content yet.
	lineHasContent  bool
	commentOnlyLine bool
}

// stripJSONC removes JSONC extensions from src and returns valid JSON.
// It strips single-line comments (//), block comments (/* */), and
// trailing commas before } and ].
//
// Comment-only lines (lines containing only whitespace and a comment)
// are removed entirely. Inline comments strip the comment but preserve
// the line break.
//
// It returns an error if a block comment is not terminated.
func stripJSONC(src []byte) ([]byte, error) {
	s := jsoncScanner{
		src: src,
		out: make([]byte, 0, len(src)),
	}
	return s.scan()
}

// scan drives the state machine to completion.
func (s *jsoncScanner) scan() ([]byte, error) {
	for s.pos < len(s.src) {
		switch s.state {
		case stateNormal:
			s.handleNormal()
		case stateString:
			s.handleString()
		case stateLineComment:
			s.handleLineComment()
		case stateBlockComment:
			s.handleBlockComment()
		}
	}

	if s.state == stateBlockComment {
		return nil, errors.New("unterminated block comment")
	}

	// Flush any remaining pending comma.
	if s.pendingComma {
		s.out = append(s.out, ',')
		s.out = append(s.out, s.pendingWS...)
	}

	return s.out, nil
}

// peek returns the byte at the current position, or 0 if at end.
func (s *jsoncScanner) peek() byte {
	if s.pos < len(s.src) {
		return s.src[s.pos]
	}
	return 0
}

// advance moves the read position forward by one.
func (s *jsoncScanner) advance() {
	s.pos++
}

// emit appends a byte to the output buffer.
func (s *jsoncScanner) emit(c byte) {
	s.out = append(s.out, c)
}

// startLineComment transitions to the line comment state, skipping the
// second '/' character.
func (s *jsoncScanner) startLineComment() {
	s.commentOnlyLine = !s.lineHasContent
	if s.commentOnlyLine {
		s.trimTrailingLineWS()
	}
	s.state = stateLineComment
	s.advance() // skip the second '/'
}

// startBlockComment transitions to the block comment state, skipping
// the '*' character.
func (s *jsoncScanner) startBlockComment() {
	s.state = stateBlockComment
	s.advance() // skip the '*'
}

// flushPendingComma writes the buffered comma and whitespace to output.
// The comma was not trailing.
func (s *jsoncScanner) flushPendingComma() {
	s.emit(',')
	s.flushWS()
	s.pendingComma = false
	s.pendingWS = s.pendingWS[:0]
}

// dropPendingComma discards the buffered comma (it was trailing) but
// preserves the buffered whitespace for indentation.
func (s *jsoncScanner) dropPendingComma() {
	s.pendingComma = false
	s.flushWS()
	s.pendingWS = s.pendingWS[:0]
}

// flushWS appends pending whitespace to output and resets lineHasContent
// on newlines.
func (s *jsoncScanner) flushWS() {
	for _, c := range s.pendingWS {
		s.emit(c)
		if c == '\n' {
			s.lineHasContent = false
		}
	}
}

// trimTrailingLineWS removes trailing spaces and tabs from the output
// buffer, for comment-only line cleanup.
func (s *jsoncScanner) trimTrailingLineWS() {
	for len(s.out) > 0 {
		last := s.out[len(s.out)-1]
		if last != ' ' && last != '\t' {
			break
		}
		s.out = s.out[:len(s.out)-1]
	}
}

// handleNormal processes a byte in the normal (non-string, non-comment) state.
func (s *jsoncScanner) handleNormal() {
	c := s.peek()
	s.advance()

	if c == '\n' {
		s.lineHasContent = false
		if s.pendingComma {
			s.pendingWS = append(s.pendingWS, c)
		} else {
			s.emit(c)
		}
		return
	}

	if c == ',' {
		s.lineHasContent = true
		if s.pendingComma {
			// Flush previous pending comma -- it was not trailing.
			s.flushPendingComma()
		}
		s.pendingComma = true
		s.pendingWS = s.pendingWS[:0]
		return
	}

	if s.pendingComma {
		s.handleNormalWithPendingComma(c)
		return
	}

	s.handleNormalChar(c)
}

// handleNormalWithPendingComma processes a byte when a comma is buffered.
// The scanner position is already past c.
func (s *jsoncScanner) handleNormalWithPendingComma(c byte) {
	// Accumulate whitespace after the pending comma.
	if c == ' ' || c == '\t' || c == '\r' {
		s.pendingWS = append(s.pendingWS, c)
		return
	}

	// Trailing comma: closing bracket follows.
	if c == '}' || c == ']' {
		s.lineHasContent = true
		s.dropPendingComma()
		s.emit(c)
		return
	}

	// Check for comment start after comma+whitespace.
	// Position is already past '/', so peek() returns the next byte.
	if c == '/' {
		next := s.peek()
		if next == '/' {
			s.advance() // skip the second '/'
			// Line comment after comma. The comma stays pending.
			// Drop whitespace between comma and comment.
			s.pendingWS = s.pendingWS[:0]
			s.state = stateLineComment
			// A comma precedes this comment, so the line has content.
			s.commentOnlyLine = false
			return
		}
		if next == '*' {
			s.advance() // skip the '*'
			// Block comment after comma. The comma stays pending.
			s.pendingWS = s.pendingWS[:0]
			s.state = stateBlockComment
			return
		}
	}

	// Not trailing -- flush the comma and whitespace, then process c.
	s.flushPendingComma()
	s.handleNormalChar(c)
}

// handleNormalChar processes a non-comma, non-newline byte outside of
// any pending-comma context. The scanner position is already past c.
func (s *jsoncScanner) handleNormalChar(c byte) {
	if c == '"' {
		s.lineHasContent = true
		s.state = stateString
		s.emit(c)
		return
	}

	// Position is already past '/', so peek() returns the next byte.
	if c == '/' {
		next := s.peek()
		if next == '/' {
			s.advance() // skip the second '/'
			s.startLineComment()
			return
		}
		if next == '*' {
			s.advance() // skip the '*'
			s.startBlockComment()
			return
		}
	}

	if !isWhitespace(c) {
		s.lineHasContent = true
	}
	s.emit(c)
}

// handleString processes a byte inside a double-quoted string.
func (s *jsoncScanner) handleString() {
	c := s.peek()
	s.advance()
	s.emit(c)

	if c == '\\' && s.pos < len(s.src) {
		// Emit the escaped character and skip it.
		s.emit(s.peek())
		s.advance()
		return
	}

	if c == '"' {
		s.state = stateNormal
	}
}

// handleLineComment discards bytes until a newline is reached.
func (s *jsoncScanner) handleLineComment() {
	c := s.peek()
	s.advance()

	if c != '\n' {
		return
	}

	s.state = stateNormal

	if s.commentOnlyLine {
		// The entire line was a comment. Don't emit the newline --
		// the line is removed entirely.
	} else {
		// Inline comment. Emit the newline to preserve line structure.
		if s.pendingComma {
			s.pendingWS = append(s.pendingWS, c)
		} else {
			s.emit(c)
		}
	}

	s.lineHasContent = false
	s.commentOnlyLine = false
}

// handleBlockComment discards bytes until the closing */ is found.
func (s *jsoncScanner) handleBlockComment() {
	c := s.peek()
	s.advance()

	if c == '*' && s.peek() == '/' {
		s.advance() // skip the '/'
		s.state = stateNormal
	}
}

// isWhitespace reports whether c is a JSON whitespace character.
func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
