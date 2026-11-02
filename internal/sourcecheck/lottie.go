package sourcecheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// checkLottie refuses a Lottie animation that is not one, one carrying an
// expression, and one loading an image or font from outside itself (#2308).
// An expression is JavaScript a full Lottie player evaluates; the portal's
// player has no expression support, and a source that relies on one would
// play wrong, so it is refused rather than stored.
func checkLottie(body string) error {
	var anim map[string]any
	if err := json.Unmarshal([]byte(body), &anim); err != nil {
		return fmt.Errorf("the Lottie animation is not valid JSON: %w", err)
	}
	if anim == nil {
		return errors.New("the Lottie animation must be a JSON object")
	}
	for _, key := range []string{"fr", "ip", "op"} {
		if _, ok := anim[key].(float64); !ok {
			return fmt.Errorf("the Lottie animation has no %q number; it needs fr, ip, op and layers", key)
		}
	}
	if _, ok := anim["layers"].([]any); !ok {
		return errors.New(`the Lottie animation has no "layers" array; it needs fr, ip, op and layers`)
	}
	var w lottieWalker
	w.walk(anim)
	if w.expressions > 0 {
		return fmt.Errorf("the Lottie animation carries %d expression(s); the portal plays animations without expressions, "+
			"so bake them into keyframes before saving", w.expressions)
	}
	if len(w.external) > 0 {
		return fmt.Errorf("the Lottie animation loads %s from outside itself; embed each image as a data:image/ URL "+
			"(export with images embedded) and leave fonts without a path", named(w.external))
	}
	return nil
}

// lottieWalker counts expressions and collects external files.
type lottieWalker struct {
	expressions int
	external    []string
}

// walk visits every value. An expression is a string under the key "x" of
// an animated property; an image asset names its file under "p" (with its
// folder under "u"); a font names a file under "fPath".
func (w *lottieWalker) walk(v any) {
	switch node := v.(type) {
	case map[string]any:
		w.visit(node)
		for _, child := range node {
			w.walk(child)
		}
	case []any:
		for _, child := range node {
			w.walk(child)
		}
	}
}

// visit records what one object of the animation carries.
func (w *lottieWalker) visit(node map[string]any) {
	if x, ok := node["x"].(string); ok && x != "" {
		w.expressions++
	}
	if p, ok := node["p"].(string); ok && node["id"] != nil && !isInlineImage(p) {
		folder, _ := node["u"].(string)
		w.external = append(w.external, folder+p)
	}
	if f, ok := node["fPath"].(string); ok && f != "" {
		w.external = append(w.external, f)
	}
}

// isInlineImage reports whether an asset's path is the image itself.
func isInlineImage(p string) bool {
	return strings.HasPrefix(strings.ToLower(p), "data:image/")
}
