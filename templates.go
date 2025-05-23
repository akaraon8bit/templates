package templates

import (
	"fmt"
	"html/template"


	"time"
  "net/url" // Add this import



"strings"
"math"
)


// Custom template functions
var TemplateFuncs = template.FuncMap{
	// Math functions
  "abs": func(x interface{}) float64 {
	switch v := x.(type) {
	case int:
		return math.Abs(float64(v))
	case float64:
		return math.Abs(v)
	default:
		return 0
	}
},
	"add":   func(a, b int) int { return a + b },
	"sub":   func(a, b int) int { return a - b },
	"div":   func(a, b int) int { return a / b },
	"mod":   func(a, b int) int { return a % b },

	// String functions
	"lower":    strings.ToLower,
	"upper":    strings.ToUpper,
	"title":    strings.Title,
	"trim":     strings.TrimSpace,
	"contains": strings.Contains,

	// URL functions
	"url":  url.QueryEscape,
	"path": url.PathEscape,

	// Date functions
	"now": time.Now,

  "ge": func(a, b interface{}) bool {
  // Handle nil values
  if a == nil || b == nil {
    return false
  }

  // Convert both to float64 for comparison
  var af, bf float64
  switch v := a.(type) {
  case int:
    af = float64(v)
  case float64:
    af = v
  default:
    return false
  }

  switch v := b.(type) {
  case int:
    bf = float64(v)
  case float64:
    bf = v
  default:
    return false
  }

  return af >= bf
},

"year": func() string {
    return fmt.Sprintf("%d", time.Now().Year())
},

"float": func(x interface{}) float64 {
	switch v := x.(type) {
	case int:
		return float64(v)
	case float64:
		return v
	default:
		return 0
	}
},
	// Custom business logic
	"formatCurrency": func(amount float64) string {
		return fmt.Sprintf("$%.2f", amount)
	},
}
