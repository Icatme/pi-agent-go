package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	core "github.com/Icatme/pi-agent-go"
)

func (r toolRuntime) executeGetTime(args getTimeArgs) (core.ToolResult, error) {
	now := r.now()
	if strings.TrimSpace(args.Timezone) == "" {
		text := fmt.Sprintf(
			"UTC: %s\nLocal (%s): %s",
			now.UTC().Format(time.RFC3339),
			r.localLocation.String(),
			now.In(r.localLocation).Format(time.RFC3339),
		)
		return core.ToolResult{
			Content: []core.Part{core.NewTextPart(text)},
			Details: map[string]any{
				"utc":            now.UTC().Format(time.RFC3339),
				"local_timezone": r.localLocation.String(),
				"local":          now.In(r.localLocation).Format(time.RFC3339),
			},
		}, nil
	}

	location, err := time.LoadLocation(strings.TrimSpace(args.Timezone))
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("get_time: invalid timezone %q", args.Timezone)
	}

	text := fmt.Sprintf("%s: %s", location.String(), now.In(location).Format(time.RFC3339))
	return core.ToolResult{
		Content: []core.Part{core.NewTextPart(text)},
		Details: map[string]any{
			"timezone": location.String(),
			"time":     now.In(location).Format(time.RFC3339),
		},
	}, nil
}

func (r toolRuntime) executeMathEval(args mathEvalArgs) (core.ToolResult, error) {
	parser := arithmeticParser{input: strings.TrimSpace(args.Expression)}
	value, err := parser.parse()
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("math_eval: %w", err)
	}

	text := strconv.FormatFloat(value, 'f', -1, 64)
	return core.ToolResult{
		Content: []core.Part{core.NewTextPart(text)},
		Details: map[string]any{
			"expression": args.Expression,
			"result":     value,
		},
	}, nil
}

type arithmeticParser struct {
	input string
	pos   int
}

func (p *arithmeticParser) parse() (float64, error) {
	if p.input == "" {
		return 0, fmt.Errorf("expression is required")
	}

	value, err := p.parseExpression()
	if err != nil {
		return 0, err
	}

	p.skipSpaces()
	if p.pos != len(p.input) {
		return 0, fmt.Errorf("unexpected token %q", p.input[p.pos:])
	}
	return value, nil
}

func (p *arithmeticParser) parseExpression() (float64, error) {
	value, err := p.parseTerm()
	if err != nil {
		return 0, err
	}

	for {
		p.skipSpaces()
		if p.match('+') {
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			value += right
			continue
		}
		if p.match('-') {
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			value -= right
			continue
		}
		return value, nil
	}
}

func (p *arithmeticParser) parseTerm() (float64, error) {
	value, err := p.parseFactor()
	if err != nil {
		return 0, err
	}

	for {
		p.skipSpaces()
		if p.match('*') {
			right, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			value *= right
			continue
		}
		if p.match('/') {
			right, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			value /= right
			continue
		}
		return value, nil
	}
}

func (p *arithmeticParser) parseFactor() (float64, error) {
	p.skipSpaces()

	if p.match('+') {
		return p.parseFactor()
	}
	if p.match('-') {
		value, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		return -value, nil
	}
	if p.match('(') {
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if !p.match(')') {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		return value, nil
	}

	return p.parseNumber()
}

func (p *arithmeticParser) parseNumber() (float64, error) {
	p.skipSpaces()
	start := p.pos
	dotSeen := false

	for p.pos < len(p.input) {
		current := rune(p.input[p.pos])
		switch {
		case unicode.IsDigit(current):
			p.pos++
		case current == '.' && !dotSeen:
			dotSeen = true
			p.pos++
		default:
			goto done
		}
	}

done:
	if start == p.pos {
		return 0, fmt.Errorf("expected number")
	}

	return strconv.ParseFloat(p.input[start:p.pos], 64)
}

func (p *arithmeticParser) skipSpaces() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *arithmeticParser) match(expected byte) bool {
	if p.pos >= len(p.input) || p.input[p.pos] != expected {
		return false
	}
	p.pos++
	return true
}
