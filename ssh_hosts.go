package main

import (
	"bufio"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func configuredSSHHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	base := filepath.Join(home, ".ssh")
	var names []string
	seenNames, seenFiles := map[string]bool{}, map[string]bool{}
	inMatch := false
	var read func(string, int)
	read = func(path string, depth int) {
		if depth > 16 || seenFiles[path] {
			return
		}
		seenFiles[path] = true
		file, err := os.Open(path)
		if err != nil {
			return
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			words := sshConfigWords(scanner.Text())
			if len(words) < 2 {
				continue
			}
			switch strings.ToLower(words[0]) {
			case "match":
				inMatch = true
			case "host":
				inMatch = false
				for _, name := range words[1:] {
					key := strings.ToLower(name)
					if hostPattern.MatchString(name) && !strings.ContainsAny(name, "*?!") && !seenNames[key] {
						seenNames[key] = true
						names = append(names, name)
					}
				}
			case "include":
				if inMatch {
					continue
				}
				for _, pattern := range words[1:] {
					if strings.HasPrefix(pattern, "~") {
						prefix, rest, _ := strings.Cut(pattern, "/")
						includeHome := home
						if prefix != "~" {
							account, err := user.Lookup(prefix[1:])
							if err != nil {
								continue
							}
							includeHome = account.HomeDir
						}
						pattern = filepath.Join(includeHome, rest)
					} else if !filepath.IsAbs(pattern) {
						pattern = filepath.Join(base, pattern)
					}
					paths, _ := filepath.Glob(pattern)
					for _, included := range paths {
						read(included, depth+1)
					}
				}
			}
		}
	}
	read(filepath.Join(base, "config"), 0)
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names
}

func sshConfigWords(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	end := strings.IndexFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r == '=' })
	if end < 0 {
		return []string{line}
	}
	words := []string{line[:end]}
	args := strings.TrimSpace(line[end:])
	args = strings.TrimSpace(strings.TrimPrefix(args, "="))
	var word strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	runes := []rune(args)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && i+1 < len(runes) {
			next := runes[i+1]
			if next == '\\' || next == '\'' || next == '"' || quote == 0 && next == ' ' {
				word.WriteRune(next)
				started = true
				i++
				continue
			}
		}
		switch {
		case quote == 0 && (r == ' ' || r == '\t'):
			flush()
		case quote == 0 && r == '#' && !started:
			return words
		case quote == 0 && (r == '\'' || r == '"'):
			quote = r
			started = true
		case quote != 0 && r == quote:
			quote = 0
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil
	}
	flush()
	return words
}
