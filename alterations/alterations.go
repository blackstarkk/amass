// Copyright 2017 Jeff Foley. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package alterations

import (
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/OWASP/Amass/stringset"
)

const (
	ldhChars = "abcdefghijklmnopqrstuvwxyz0123456789-"
)

// Cache maintains counters for word usage within alteration techniques.
type Cache struct {
	sync.RWMutex
	Counters map[string]int
}

// NewCache returns an initialized Cache.
func NewCache(seed []string) *Cache {
	c := &Cache{Counters: make(map[string]int)}

	for _, word := range seed {
		c.Counters[word] = 0
	}

	return c
}

// Update increments the count for the provided word.
func (c *Cache) Update(word string) int {
	c.Lock()
	defer c.Unlock()

	if _, ok := c.Counters[word]; ok {
		c.Counters[word]++
	} else {
		c.Counters[word] = 1
	}

	return c.Counters[word]
}

// State maintains the word prefix and suffix counters.
type State struct {
	MinForWordFlip int
	EditDistance   int
	Prefixes       *Cache
	Suffixes       *Cache
}

// NewState returns an initialized State.
func NewState(wordlist []string) *State {
	return &State{
		Prefixes: NewCache(wordlist),
		Suffixes: NewCache(wordlist),
	}
}

// FlipWords flips prefixes and suffixes found within the provided name.
func (s *State) FlipWords(name string) []string {
	names := strings.SplitN(name, ".", 2)
	subdomain := names[0]
	domain := names[1]

	parts := strings.Split(subdomain, "-")
	if len(parts) < 2 {
		return []string{}
	}

	newNames := stringset.New()

	pre := parts[0]
	s.Prefixes.Update(pre)
	s.Prefixes.RLock()
	for k, count := range s.Prefixes.Counters {
		if count >= s.MinForWordFlip {
			newNames.Insert(k + "-" + strings.Join(parts[1:], "-") + "." + domain)
		}
	}
	s.Prefixes.RUnlock()

	post := parts[len(parts)-1]
	s.Suffixes.Update(post)
	s.Suffixes.RLock()
	for k, count := range s.Suffixes.Counters {
		if count >= s.MinForWordFlip {
			newNames.Insert(strings.Join(parts[:len(parts)-1], "-") + "-" + k + "." + domain)
		}
	}
	s.Suffixes.RUnlock()

	return newNames.Slice()
}

// FlipNumbers flips numbers in a subdomain name.
func (s *State) FlipNumbers(name string) []string {
	n := name
	parts := strings.SplitN(n, ".", 2)

	// Find the first character that is a number
	first := strings.IndexFunc(parts[0], unicode.IsNumber)
	if first < 0 {
		return []string{}
	}

	newNames := stringset.New()

	// Flip the first number and attempt a second number
	for i := 0; i < 10; i++ {
		sf := n[:first] + strconv.Itoa(i) + n[first+1:]

		newNames.InsertMany(s.secondNumberFlip(sf, first+1)...)
	}

	// Take the first number out
	newNames.InsertMany(s.secondNumberFlip(n[:first]+n[first+1:], -1)...)

	return newNames.Slice()
}

func (s *State) secondNumberFlip(name string, minIndex int) []string {
	parts := strings.SplitN(name, ".", 2)

	// Find the second character that is a number
	last := strings.LastIndexFunc(parts[0], unicode.IsNumber)
	if last < 0 || last < minIndex {
		return []string{name}
	}

	var newNames []string
	// Flip those numbers and send out the mutations
	for i := 0; i < 10; i++ {
		n := name[:last] + strconv.Itoa(i) + name[last+1:]

		newNames = append(newNames, n)
	}

	// Take the second number out
	newNames = append(newNames, name[:last]+name[last+1:])

	return newNames
}

// AppendNumbers appends a number to a subdomain name.
func (s *State) AppendNumbers(name string) []string {
	parts := strings.SplitN(name, ".", 2)

	parts[0] = strings.Trim(parts[0], "-")
	if parts[0] == "" {
		return []string{}
	}

	newNames := stringset.New()
	for i := 0; i < 10; i++ {
		newNames.InsertMany(s.addSuffix(parts, strconv.Itoa(i))...)
	}

	return newNames.Slice()
}

// AddSuffixWord appends a suffix to a subdomain name.
func (s *State) AddSuffixWord(name string) []string {
	parts := strings.SplitN(name, ".", 2)

	s.Suffixes.RLock()
	defer s.Suffixes.RUnlock()

	parts[0] = strings.Trim(parts[0], "-")
	if parts[0] == "" {
		return []string{}
	}

	newNames := stringset.New()
	for word, count := range s.Suffixes.Counters {
		if count >= s.MinForWordFlip {
			newNames.InsertMany(s.addSuffix(parts, word)...)
		}
	}

	return newNames.Slice()
}

// AddPrefixWord appends a subdomain name to a prefix.
func (s *State) AddPrefixWord(name string) []string {
	s.Prefixes.RLock()
	defer s.Prefixes.RUnlock()

	name = strings.Trim(name, "-")
	if name == "" {
		return []string{}
	}

	newNames := stringset.New()
	for word, count := range s.Prefixes.Counters {
		if count >= s.MinForWordFlip {

			newNames.InsertMany(s.addPrefix(name, word)...)
		}
	}

	return newNames.Slice()
}

func (s *State) addSuffix(parts []string, suffix string) []string {
	return []string{
		parts[0] + suffix + "." + parts[1],
		parts[0] + "-" + suffix + "." + parts[1],
	}
}

func (s *State) addPrefix(name, prefix string) []string {
	return []string{
		prefix + name,
		prefix + "-" + name,
	}
}

// FuzzyLabelSearches returns new names generated by making slight
// mutations to the provided name.
func (s *State) FuzzyLabelSearches(name string) []string {
	parts := strings.SplitN(name, ".", 2)

	results := []string{parts[0]}
	for i := 0; i < s.EditDistance; i++ {
		var conv []string

		conv = append(conv, s.additions(results)...)
		conv = append(conv, s.deletions(results)...)
		conv = append(conv, s.substitutions(results)...)
		results = append(results, conv...)
	}

	newNames := stringset.New()
	for _, alt := range results {
		label := strings.Trim(alt, "-")
		if label == "" {
			continue
		}

		newNames.Insert(label + "." + parts[1])
	}

	return newNames.Slice()
}

func (s *State) additions(set []string) []string {
	ldh := []rune(ldhChars)
	ldhLen := len(ldh)

	var results []string
	for _, str := range set {
		rstr := []rune(str)
		rlen := len(rstr)

		for i := 0; i <= rlen; i++ {
			for j := 0; j < ldhLen; j++ {
				temp := append(rstr, ldh[0])

				copy(temp[i+1:], temp[i:])
				temp[i] = ldh[j]
				results = append(results, string(temp))
			}
		}
	}
	return results
}

func (s *State) deletions(set []string) []string {
	var results []string

	for _, str := range set {
		rstr := []rune(str)
		rlen := len(rstr)

		for i := 0; i < rlen; i++ {
			if del := string(append(rstr[:i], rstr[i+1:]...)); del != "" {
				results = append(results, del)
			}
		}
	}
	return results
}

func (s *State) substitutions(set []string) []string {
	ldh := []rune(ldhChars)
	ldhLen := len(ldh)

	var results []string
	for _, str := range set {
		rstr := []rune(str)
		rlen := len(rstr)

		for i := 0; i < rlen; i++ {
			temp := rstr

			for j := 0; j < ldhLen; j++ {
				temp[i] = ldh[j]
				results = append(results, string(temp))
			}
		}
	}
	return results
}
