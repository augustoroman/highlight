package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/augustoroman/ansi" // change back to "github.com/mgutz/ansi" when PR is accepted
	"github.com/fluxio/iohelpers/line"
)

var usage = `
Usage: highlight <config> <patterns...> [<config> <patterns...>]...

Highlight accepts a sequence of regex patterns to match the input against
and highlight pattern matches.  You can optionally also specify configuration
flags that affect how subsequent patterns are highlighted.

Configuration options are:
  -w <color>   Following patterns apply color to matching words.
  -l <color>   Following patterns apply color to matching lines.
  -lx <color>  Following patterns apply color to NON-matching lines.

For line matching, the first pattern to match applies.
For word matching, the first pattern to match applies.  Overlapping patterns
are applied with the first match taking precedence.

In addition, the following configuration options are independent of patterns:
  -c <color>   Set the default color for all unmatched text.  If specified
               multiple times, the last one takes precedence.

  --debug      Escape all output, no colors are printed but color codes are
               visible.

Colors:
  The general form of colors is:
    FG[+mod][:BG[+h]]
  where FG and BG are colors with optional modifers after a '+'.

  Colors may be specified by name:
    black  red  green  yellow  blue  magenta  cyan  white  default
  Or via the 256 color palette number:
    0 1 2 ...

  Modifiers may be combinations of:
    d = dim
    h = high-intensity
    b = bold
    u = underline
    i = inverse
    s = strikethrough
    B = blink

  h (high-intensity) is the only modifier that can be used for background
  colors.

  red            -> red
  red+b          -> red bold
  red+B          -> red blinking
  red+u          -> red underline
  red+bh         -> red bold bright
  red:white      -> red on white
  red+b:white+h  -> red bold on white bright
  red+B:white+h  -> red blink on white bright
  black+hd       -> dark gray
  white+d        -> dim white
  yellow+hb      -> bright, bold yellow
  red+u:blue+h   -> underlined red on a bright blue background

Examples:

  Based on a file 'test.txt' with content:
    The quick brown fox jumped
    over the lazy dog.  Pack my box
    with five dozen liquor jugs.

  cat test.txt | highlight '[Tt]he' '\w{5}'
    This will highlight 'The', 'quick' 'brown' and 'jumpe' in the first line,
    'the' on the second line, and 'dozen' and 'liquo' on the third.

  cat test.txt | highlight -c blue -l yellow lazy -w yellow+bh lazy fox
    This will print lines in blue unless they contain 'lazy'.  Lines containing
    'lazy' will be printed in yellow.  The text 'lazy' itself is bright bold
    yellow, as is the text 'fox' on any line.

  cat test.txt | highlight -c blue -l yellow lazy -l red:blue dog fox -w green 'over.{8}'
    This will print lines in blue by default.  Lines containing 'lazy' will be
    printed in yellow, otherwise lines containig 'dog' or 'fox' will be printed
    in red with blue background.  Text matching 'over.{8}' will be printed in
    green. The final effect will be the first line red, the second line starts
    with green and ends in yellow, and the last line will be in the default blue.
`

func main() {
	var DefaultWordHighlightColor = ansi.LightBlue
	log.SetFlags(0)

	colorizer := &ColorizerWriter{Out: os.Stdout}

	// The current rule as we are parsing the command-line.  This may be either a
	// WordRule or a LineRule.
	var current Rule

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "-") {
			mode := strings.TrimLeft(arg, "-")

			if mode == "h" || mode == "help" {
				log.Println(usage)
				os.Exit(0)
			} else if mode == "debug" {
				colorizer.Out = EscapingWriter{os.Stdout}
				continue
			}

			i++
			if i == len(os.Args) {
				break
			}
			color := ansi.ColorCode(os.Args[i])

			switch mode {
			case "l":
				colorizer.AddRuleIfNotNil(current)
				current = &LineRule{Color: color}
			case "lx":
				colorizer.AddRuleIfNotNil(current)
				current = &LineRule{Color: color, Inverse: true}
			case "w":
				colorizer.AddRuleIfNotNil(current)
				current = &WordRule{Color: color}
			case "c":
				colorizer.DefaultColor = color
			default:
				log.Fatalf("%s\n%sERROR: No such mode: %q",
					usage, ansi.Red, mode)
			}
		} else {
			if current == nil {
				current = &WordRule{Color: DefaultWordHighlightColor}
			}
			pattern, err := regexp.Compile(arg)
			if err != nil {
				log.Fatalf("%s\n%sERROR: Bad matching pattern %q: %v",
					usage, ansi.Red, arg, err)
			}
			current.AddPattern(pattern)
		}
	}
	colorizer.AddRuleIfNotNil(current)

	// BoundaryWriter allows us to ensure that we don't write parts of lines.
	out := &line.BoundaryWriter{Target: colorizer}
	_, err := io.Copy(out, os.Stdin)
	if err != nil {
		log.Fatal(ansi.Red + err.Error())
	}
}

type EscapingWriter struct{ Out io.Writer }

func (e EscapingWriter) Write(p []byte) (int, error) {
	newline := []byte("\n")
	for _, line := range bytes.SplitAfter(p, newline) {
		hasNewline := bytes.HasSuffix(line, newline)
		if hasNewline {
			line = line[:len(line)-1]
		}
		quoted := strconv.Quote(string(line))

		// Strip the leading and trailing quotation marks: I want all
		// the escaping, but not actually the quoting.
		quoted = quoted[1 : len(quoted)-1]

		io.WriteString(e.Out, quoted)
		if hasNewline {
			e.Out.Write(newline)
		}
	}
	return len(p), nil
}

type Rule interface {
	AddPattern(*regexp.Regexp)
}

type WordRule struct {
	Color    string
	Patterns []*regexp.Regexp
}
type LineRule struct {
	Inverse  bool
	Color    string
	Patterns []*regexp.Regexp
}

func (w *WordRule) AddPattern(pattern *regexp.Regexp) { w.Patterns = append(w.Patterns, pattern) }
func (l *LineRule) AddPattern(pattern *regexp.Regexp) { l.Patterns = append(l.Patterns, pattern) }

type ColorizerWriter struct {
	DefaultColor string
	WordRules    []WordRule
	LineRules    []LineRule
	Out          io.Writer
}

func (c *ColorizerWriter) AddRuleIfNotNil(rule interface{}) {
	if rule == nil {
		return
	}
	switch r := rule.(type) {
	case *LineRule:
		c.LineRules = append(c.LineRules, *r)
	case *WordRule:
		c.WordRules = append(c.WordRules, *r)
	default:
		log.Fatalf("Unknown rule type: %T", rule)
	}
}

func (c *ColorizerWriter) Write(data []byte) (int, error) {
	var err error
	var n, written int
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		n, err = c.WriteOneLine(line)
		written += n
		if err != nil {
			break
		}
	}
	return len(data), err
}

func (c *ColorizerWriter) WriteOneLine(line []byte) (int, error) {
	N := len(line)
	written := 0

	hasNewline := bytes.HasSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\n"))
	lineCol := c.pickLineColor(line)
	if lineCol != "" {
		n, err := c.Out.Write([]byte(lineCol))
		if err != nil {
			return n, err
		}
		written += n
	} else {
		lineCol = ansi.Reset // we should reset for each word if no line col
	}
	line = c.applyWordRules(line, lineCol)

	n, err := c.Out.Write(line)
	if err != nil {
		return n + written, err
	}
	written += n

	if lineCol != ansi.Reset {
		n, err = c.Out.Write([]byte(ansi.Reset))
		written += n
	}
	if hasNewline {
		_, err = c.Out.Write([]byte("\n"))
	}
	return N, err
}

func (c *ColorizerWriter) pickLineColor(line []byte) string {
	for _, rule := range c.LineRules {
		for _, pat := range rule.Patterns {
			colorizeLine := pat.Match(line)
			if rule.Inverse {
				colorizeLine = !colorizeLine
			}
			if colorizeLine {
				return rule.Color
			}
		}
	}
	return c.DefaultColor
}

func (c *ColorizerWriter) applyWordRules(line []byte, lineColor string) []byte {
	const (
		START = iota
		STOP
	)
	type event struct {
		typ   int // true if the color is starting, false if ending
		color string
		pos   int
	}
	var events []event

	NUM_RULES := len(c.WordRules)
	for i := range c.WordRules {
		rule := c.WordRules[NUM_RULES-i-1]
		for _, pat := range rule.Patterns {
			for _, pos := range pat.FindAllIndex(line, -1) {
				events = append(events,
					event{START, rule.Color, pos[0]},
					event{STOP, rule.Color, pos[1]})
			}
		}
	}

	if len(events) == 0 {
		return line // no changes, no need to copy the line
	}

	// Sort the events by position. This will split up the start/stop events.
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].pos < events[j].pos
	})

	colorStack := []string{lineColor}
	var lineOut []byte
	cur := 0 // current position in the original line

	for _, e := range events {
		lineOut = append(lineOut, line[cur:e.pos]...)
		color := e.color
		if e.typ == START {
			// Push e.color onto the color stack, it's now the latest color.
			colorStack = append(colorStack, e.color)
		} else {
			// Pop e.color from the color stack.  It has to be on the stack somewhere,
			// but if another overlapping pattern has been pushed in the meantime then
			// it won't be the last item on the stack.  Since it's almost certainly
			// vert recent and it's likely the color stack is very shallow, just do a
			// reverse linear search through the stack looking for this color.
			// In fact, since most cases won't be overlapping patterns, this loop will
			// probably execute exactly one iteration.
			N := len(colorStack)
			var pos int
			// Use pos > 0 because at worst we end up with pos = 0.
			for pos = N - 1; pos > 0; pos-- {
				if colorStack[pos] == e.color {
					break
				}
			}
			// When we find it, shift the stack down on top of it.  As mentioned
			// earlier, pos will probably be the last entry of the stack and therefore
			// this loop won't have any iterations.
			for j := pos + 1; j < N; j++ {
				colorStack[j-1] = colorStack[j]
			}
			// Shorten the stack.
			colorStack = colorStack[:N-1]

			tail := N - 2
			color = colorStack[tail]
		}
		lineOut = append(lineOut, []byte(color)...)
		cur = e.pos
	}

	// Copy whatever remains in the original line.
	lineOut = append(lineOut, line[cur:]...)

	return lineOut
}
