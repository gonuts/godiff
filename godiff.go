//
//  File/Directory diff tool with HTML output
//  Copyright (C) 2012   Siu Pin Chao
//
//  This program is free software: you can redistribute it and/or modify
//  it under the terms of the GNU General Public License as published by
//  the Free Software Foundation, either version 3 of the License, or
//  (at your option) any later version.
//
//  This program is distributed in the hope that it will be useful,
//  but WITHOUT ANY WARRANTY; without even the implied warranty of
//  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//  GNU General Public License for more details.
//
//  You should have received a copy of the GNU General Public License
//  along with this program.  If not, see <http://www.gnu.org/licenses/>.
//
// Description:
//  This program can be use to compare files and directories for differences.
//  When comparing directories, it iterates through all files in both directories
//  and compare files having the same name.
//
//  It uses the algorithm from "An O(ND) Difference Algorithm and its Variations"
//  by Eugene Myers Algorithmica Vol. 1 No. 2, 1986, p 251.
//
// Main Features:
//  * Supports UTF8 file.
//  * Show differences within a line
//  * Options for ignore case, white spaces compare, blank lines etc.
//
// Main aim of the application is to try out the features in the go programming language. (golang.org)
//  * Slices: Used extensively, and re-slicing too whenever it make sense.
//  * File I/O: Use Mmap for reading text files
//  * Function Closure: Use in callbacks functions to handle both file and line compare
//  * Goroutines: for running multiple file compares concurrently, using channels and mutex too.
//
//
//  History
//  -------
//  2012/09/20  Created
//
//
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"hash/crc32"
	"html"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// Version number
	VERSION = "0.02"

	// Scan at up to this size in file for '\0' in test for binary file
	BINARY_CHECK_SIZE = 65536

	// Output buffer size
	OUTPUT_BUF_SIZE = 65536

	// default number of context lines to display
	CONTEXT_LINES = 3

	// convenient shortcut
	PATH_SEPARATOR = string(os.PathSeparator)

	// use mmap for file greather than this size, for smaller files just use Read() instead.
	MMAP_THRESHOLD = 8 * 1024
)

// Error Messages
const (
	MSG_FILE_SIZE_ZERO   = "File has size 0"
	MSG_FILE_NOT_EXISTS  = "File does not exist"
	MSG_DIR_NOT_EXISTS   = "Directory does not exist"
	MSG_FILE_IS_BINARY   = "This is a binary file"
	MSG_FILE_DIFFERS     = "File differs"
	MSG_BIN_FILE_DIFFERS = "File differs. This is a binary file"
	MSG_FILE_IDENTICAL   = "Files are the same"
	MSG_FILE_TOO_BIG     = "File too big"
	MSG_THIS_IS_DIR      = "This is a directory"
	MSG_THIS_IS_FILE     = "This is a file"
)

// file data
type Filedata struct {
	name      string
	info      os.FileInfo
	osfile    *os.File
	errormsg  string
	is_binary bool
	is_mapped bool
	data      []byte
}

// Output to diff as html or text format
type OutputFormat struct {
	line1_start, line1_end int
	line2_start, line2_end int
	buf1, buf2             bytes.Buffer
	name1, name2           string
	fileinfo1, fileinfo2   os.FileInfo
	header_printed         bool
}

// Interface for diff report_change() callbacks.
type DiffChanger interface {
	diff_same(int, int, int, int)
	diff_modify(int, int, int, int)
	diff_insert(int, int, int, int)
	diff_remove(int, int, int, int)
}

// For changes within a line
type DiffChangeLine struct {
	outfmt       *OutputFormat
	line1, line2 []byte
	pos1, pos2   []int
}

// changes to be output in Text format
type DiffChangeFileText struct {
	outfmt       *OutputFormat
	file1, file2 [][]byte
}

// changes to be output in Html format
type DiffChangeFileHtml struct {
	outfmt       *OutputFormat
	file1, file2 [][]byte
}

const HTML_HEADER = `<!doctype html><html><head>
<meta http-equiv="content-type" content="text/html;charset=utf-8">`

const HTML_CSS = `<style type="text/css">
.tab {border-color:#808080; border-style:solid; border-width:1px 1px 1px 1px; border-collapse:collapse;}
.tth {border-color:#808080; border-style:solid; border-width:1px 1px 1px 1px; border-collapse:collapse; padding:4px; vertical-align:top; text-align:left; background-color:#E0E0E0;}
.ttd {border-color:#808080; border-style:solid; border-width:1px 1px 1px 1px; border-collapse:collapse; padding:4px; vertical-align:top; text-align:left;}
.hdr {color:black; font-size:85%;}
.inf {color:#C08000; font-size:85%;}
.err {color:red; font-size:85%; font-style:bold; margin:0; display:block;}
.msg {color:#508050; font-size:85%; font-style:bold; margin:0; display:block;}
.lin {color:#C08000; font-size:75%; font-style:italic; margin:0; display:block;}
.nop {color:black; font-size:75%; font-family:monospace; white-space:pre; margin:0; display:block;}
.upd {color:black; font-size:75%; font-family:monospace; white-space:pre; margin:0; background-color:#CFCFFF; display:block;}
.chg {color:#C00080;}
.add {color:black; font-size:75%; font-family:monospace; white-space:pre; margin:0; background-color:#CFFFCF; display:block;}
.del {color:black; font-size:75%; font-family:monospace; white-space:pre; margin:0; background-color:#FFCFCF; display:block;}
</style>`

const HTML_LEGEND = `<br><b>Legend:</b><br><table class="tab">
<tr><td class="tth"><span class="hdr">filename 1</span></td><td class="tth"><span class="hdr">filename 2</span></td></tr>
<tr><td class="ttd">
<span class="lin">Line N</span>
<span class="del">  line deleted</span>
<span class="nop">  no change</span>
<span class="upd">  line modified</span>
</td>
<td class="ttd">
<span class="lin">Line M</span>
<span class="add">  line added</span>
<span class="nop">  no change</span>
<span class="upd">  <span class="chg">L</span>ine <span class="chg">M</span>odified</span>
</td></tr>
</table>
`

// command line arguments
var (
	flag_pprof_file              string
	flag_version                 bool = false
	flag_cmp_ignore_case         bool = false
	flag_cmp_ignore_blank_lines  bool = false
	flag_cmp_ignore_space_change bool = false
	flag_cmp_ignore_all_space    bool = false
	flag_unicode_case_and_space  bool = false
	flag_show_identical_files    bool = false
	flag_suppress_line_changes   bool = false
	flag_suppress_missing_file   bool = false
	flag_output_as_text          bool = false
	flag_context_lines           int  = CONTEXT_LINES
	flag_max_goroutines               = 1
)

// Job queue for goroutines
type JobQueue struct {
	name1, name2 string
	info1, info2 os.FileInfo
}

// Queue queue for goroutines diff_file
var (
	job_queue chan JobQueue
	job_wait  sync.WaitGroup
)

// Buffered stdout
var (
	out      = bufio.NewWriterSize(os.Stdout, OUTPUT_BUF_SIZE)
	out_lock sync.Mutex
)

// html entity strings
var (
	html_entity_amp    = html.EscapeString("&")
	html_entity_gt     = html.EscapeString(">")
	html_entity_lt     = html.EscapeString("<")
	html_entity_squote = html.EscapeString("'")
	html_entity_dquote = html.EscapeString("\"")
)

// functions to compare line and computer hash values,
// these will be setup based on flags: -b -w -U etc.
var (
	compare_line func([]byte, []byte) bool
	compute_hash func([]byte) uint32
)

var blank_line = make([]byte, 0)

func version() {
	fmt.Printf("godiff. Version %s\n", VERSION)
	fmt.Printf("Copyright (C) 2012 Siu Pin Chao.\n")
}

func usage(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, "%s\n", msg)
	}
	fmt.Fprint(os.Stderr, "A text file comparison tool displaying differenes in HTML\n\n")
	fmt.Fprint(os.Stderr, "usage: godiff <options> <file|dir> <file|dir>\n")
	flag.PrintDefaults()
	os.Exit(2)
}

func usage0() {
	usage("")
}

// Main routine.
func main() {

	// setup command line options
	flag.Usage = usage0
	flag.StringVar(&flag_pprof_file, "prof", "", "Write pprof output to file")
	flag.BoolVar(&flag_version, "v", flag_version, "Print version information")
	flag.IntVar(&flag_context_lines, "c", flag_context_lines, "Include N lines of context before and after changes")
	flag.IntVar(&flag_max_goroutines, "g", flag_max_goroutines, "Max number of goroutines to use for file comparison")
	flag.BoolVar(&flag_cmp_ignore_space_change, "b", flag_cmp_ignore_space_change, "Ignore changes in the amount of white space")
	flag.BoolVar(&flag_cmp_ignore_all_space, "w", flag_cmp_ignore_all_space, "Ignore all white space")
	flag.BoolVar(&flag_cmp_ignore_case, "i", flag_cmp_ignore_case, "Ignore case differences in file contents")
	flag.BoolVar(&flag_cmp_ignore_blank_lines, "B", flag_cmp_ignore_blank_lines, "Ignore changes whose lines are all blank")
	flag.BoolVar(&flag_unicode_case_and_space, "unicode", flag_unicode_case_and_space, "Apply unicode rules for white space and upper/lower case")
	flag.BoolVar(&flag_show_identical_files, "s", flag_show_identical_files, "Report when two files are the identical")
	flag.BoolVar(&flag_suppress_line_changes, "l", flag_suppress_line_changes, "Do not display changes within lines")
	flag.BoolVar(&flag_suppress_missing_file, "m", flag_suppress_missing_file, "Do not show content if corresponding file is missing")
	flag.BoolVar(&flag_output_as_text, "n", flag_output_as_text, "Output using 'diff' text format instead of HTML")
	flag.Parse()

	if flag_version {
		version()
		os.Exit(0)
	}

	// write pprof info
	if flag_pprof_file != "" {
		pf, err := os.Create(flag_pprof_file)
		if err != nil {
			usage(err.Error())
		}
		pprof.StartCPUProfile(pf)
		defer pprof.StopCPUProfile()
	}

	// flush output on termination
	defer func() {
		out.Flush()
	}()

	// choose which compare and hash function to use
	if flag_cmp_ignore_case || flag_cmp_ignore_space_change || flag_cmp_ignore_all_space {
		if flag_unicode_case_and_space {
			compute_hash = compute_hash_unicode
			compare_line = compare_line_unicode
		} else {
			compute_hash = compute_hash_bytes
			compare_line = compare_line_bytes
		}
	} else {
		compute_hash = compute_hash_exact
		compare_line = bytes.Equal
	}

	// get command line args
	args := flag.Args()
	if len(args) < 2 {
		usage("Missing files")
	}

	if len(args) > 2 {
		usage("Too many files")
	}

	// get the directory name or filename
	file1, file2 := args[0], args[1]

	// check file type
	finfo1, err1 := os.Stat(file1)
	finfo2, err2 := os.Stat(file2)

	// Unable to find either file/directory
	if err1 != nil || err2 != nil {
		if err1 != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err1.Error())
		}
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err2.Error())
		}
		os.Exit(1)
	}

	if finfo1.IsDir() != finfo2.IsDir() {
		usage("Unable to compare file and directory")
	}

	if !flag_output_as_text {
		out.WriteString(HTML_HEADER)
		fmt.Fprintf(out, "<title>Compare %s vs %s</title>\n", html.EscapeString(file1), html.EscapeString(file2))
		out.WriteString(HTML_CSS)
		out.WriteString("</head><body>\n")
		fmt.Fprintf(out, "<h1>Compare %s vs %s</h1><br>\n", html.EscapeString(file1), html.EscapeString(file2))
	}

	switch {
	case !finfo1.IsDir() && !finfo2.IsDir():
		diff_file(file1, file2, finfo1, finfo2)

	case finfo1.IsDir() && finfo2.IsDir():
		job_queue_init()
		diff_dirs(file1, file2, finfo1, finfo2)
		job_queue_finish()
	}

	if !flag_output_as_text {
		fmt.Fprintf(out, "Generated on %s<br>", time.Now().Format(time.RFC1123))
		out.WriteString(HTML_LEGEND)
		out.WriteString("</body></html>\n")
	}
}

//
// Call the diff algorithm.
//
func do_diff(data1, data2 []int) ([]bool, []bool) {
	len1, len2 := len(data1), len(data2)
	change1, change2 := make([]bool, len1), make([]bool, len2)

	size := (len1+len2+1)*2 + 2
	v := make([]int, size*2)

	// Run diff compare algorithm.
	algorithm_lcs(data1, data2, change1, change2, v)

	return change1, change2
}

//
// Find the begin/end of this 'changed' segment
//
func next_change_segment(start *int, change []bool, data []int) (int, int) {

	// find the end of this changes segment
	end := *start + 1
	for end < len(change) && change[end] {
		end++
	}

	// skip blank lines in the begining and end of the changes
	i, j := *start, end
	for i < end && data[i] == 0 {
		i++
	}
	for j > i && data[j-1] == 0 {
		j--
	}

	*start = end
	return i, j
}

//
// Report diff changes.
// For each type of change, call the corresponding insert/modify/remove/same function
//
func report_changes(chg DiffChanger, data1, data2 []int, change1, change2 []bool) bool {
	len1, len2 := len(change1), len(change2)
	i1, i2 := 0, 0
	changed := false

	// scan for changes
	for i1 < len1 || i2 < len2 {
		switch {
		// no change, advance both i1 and i2 to to next set of changes
		case i1 < len1 && i2 < len2 && !change1[i1] && !change2[i2]:
			s1, s2 := i1+1, i2+1
			for s1 < len1 && s2 < len2 && !change1[s1] && !change2[s2] {
				s1, s2 = s1+1, s2+1
			}
			chg.diff_same(i1, s1, i2, s2)
			i1, i2 = s1, s2

		// change in both lists
		case i1 < len1 && i2 < len2 && change1[i1] && change2[i2]:
			m1start, m1end := next_change_segment(&i1, change1, data1)
			m2start, m2end := next_change_segment(&i2, change2, data2)

			if m1start < m1end && m2start < m2end {
				chg.diff_modify(m1start, m1end, m2start, m2end)
				changed = true
			} else if m1start < m1end {
				chg.diff_remove(m1start, m1end, m2start, m2end)
				changed = true
			} else if m2start < m2end {
				chg.diff_insert(m1start, m1end, m2start, m2end)
				changed = true
			}

		case i1 < len1 && change1[i1]:
			m1start, m1end := next_change_segment(&i1, change1, data1)
			if m1start < m1end {
				chg.diff_remove(m1start, m1end, i2, i2)
				changed = true
			}

		case i2 < len2 && change2[i2]:
			m2start, m2end := next_change_segment(&i2, change2, data2)
			if m2start < m2end {
				chg.diff_insert(i1, i1, m2start, m2end)
				changed = true
			}

		default: // should not reach here
			return true
		}
	}
	return changed
}

// convert byte to lower case
func to_lower_byte(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b - 'A' + 'a'
	}
	return b
}

// split text into array of individual rune position, and another array for comparison.
func split_runes(s []byte) ([]int, []int) {

	pos := make([]int, len(s)+1)
	cmp := make([]int, len(s))

	var h, i, n int

	for i < len(s) {
		pos[n] = i
		b := s[i]
		if b < utf8.RuneSelf {
			if flag_cmp_ignore_case {
				if flag_unicode_case_and_space {
					h = int(unicode.ToLower(rune(b)))
				} else {
					h = int(to_lower_byte(b))
				}
			} else {
				h = int(b)
			}
			i++
		} else {
			r, rsize := utf8.DecodeRune(s[i:])
			if flag_cmp_ignore_case && flag_unicode_case_and_space {
				h = int(unicode.ToLower(r))
			} else {
				h = int(r)
			}
			i += rsize
		}
		cmp[n] = h
		n = n + 1
	}
	pos[n] = i
	return pos[:n+1], cmp[:n]
}

//
// Write bytes to buffer, ready to be output as html,
// replace special chars with html-entities
//
func write_html_bytes(buf *bytes.Buffer, line []byte) {

	esc := ""
	lasti := 0

	for i, v := range line {

		// this this faster than using swtch/case
		if v == '<' {
			esc = html_entity_lt
		} else if v == '>' {
			esc = html_entity_gt
		} else if v == '&' {
			esc = html_entity_amp
		} else if v == '\'' {
			esc = html_entity_squote
		} else if v == '"' {
			esc = html_entity_dquote
		} else {
			continue
		}

		buf.Write(line[lasti:i])
		buf.WriteString(esc)
		lasti = i + 1
	}

	buf.Write(line[lasti:])
}

func output_diff_message(filename1, filename2 string, info1, info2 os.FileInfo, msg1, msg2 string, is_error bool) {
	output_diff_message_content(filename1, filename2, info1, info2, msg1, msg2, nil, nil, is_error)
}

func output_diff_message_content(filename1, filename2 string, info1, info2 os.FileInfo, msg1, msg2 string, data1, data2 []byte, is_error bool) {

	if flag_output_as_text {
		out_acquire_lock()
		fmt.Fprintf(out, "#< %s: %s\n", filename1, msg1)
		fmt.Fprintf(out, "#> %s: %s\n\n", filename2, msg2)
		out_release_lock()
	} else {

		outfmt := OutputFormat{
			name1:     filename1,
			name2:     filename2,
			fileinfo1: info1,
			fileinfo2: info2,
		}

		var id string
		if is_error {
			id = "err"
		} else {
			id = "msg"
		}

		if msg1 != "" {
			outfmt.buf1.WriteString("<span class=\"")
			outfmt.buf1.WriteString(id)
			outfmt.buf1.WriteString("\">")
			write_html_bytes(&outfmt.buf1, []byte(msg1))
			outfmt.buf1.WriteString("</span>")
		} else if data1 != nil && len(data1) > 0 {
			outfmt.buf1.WriteString("<span class=\"nop\">")
			write_html_bytes(&outfmt.buf1, data1)
			outfmt.buf1.WriteString("</span>")
		}

		if msg2 != "" {
			outfmt.buf2.WriteString("<span class=\"")
			outfmt.buf2.WriteString(id)
			outfmt.buf2.WriteString("\">")
			write_html_bytes(&outfmt.buf2, []byte(msg2))
			outfmt.buf2.WriteString("</span>")
		} else if data2 != nil && len(data2) > 0 {

			outfmt.buf2.WriteString("<span class=\"nop\">")
			write_html_bytes(&outfmt.buf2, data2)
			outfmt.buf2.WriteString("</span>")
		}

		html_add_block(&outfmt)
		if outfmt.header_printed {
			out.WriteString("</table><br>\n")
			outfmt.header_printed = false
			out_release_lock()
		}
	}
}

func html_add_block(outfmt *OutputFormat) {

	if outfmt.buf1.Len() > 0 || outfmt.buf2.Len() > 0 || outfmt.line1_start < outfmt.line1_end || outfmt.line2_start < outfmt.line2_end {

		if !outfmt.header_printed {
			out_acquire_lock()
			outfmt.header_printed = true
			out.WriteString("<table class=\"tab\"><tr><td class=\"tth\"><span class=\"hdr\">")
			out.WriteString(html.EscapeString(outfmt.name1))
			out.WriteString("</span>")
			if outfmt.fileinfo1 != nil {
				fmt.Fprintf(out, "<br><span class=\"inf\">%d %s</span>", outfmt.fileinfo1.Size(), outfmt.fileinfo1.ModTime().Format(time.RFC1123))
			}

			out.WriteString("</td><td class=\"tth\"><span class=\"hdr\">")
			out.WriteString(html.EscapeString(outfmt.name2))
			out.WriteString("</span>")
			if outfmt.fileinfo2 != nil {
				fmt.Fprintf(out, "<br><span class=\"inf\">%d %s</span>", outfmt.fileinfo2.Size(), outfmt.fileinfo2.ModTime().Format(time.RFC1123))
			}
			out.WriteString("</td></tr>")
		}

		out.WriteString("<tr><td class=\"ttd\">")
		if outfmt.line1_start < outfmt.line1_end {
			fmt.Fprintf(out, "<span class=\"lin\">Line %d to %d</span>", outfmt.line1_start+1, outfmt.line1_end)
		}
		out.Write(outfmt.buf1.Bytes())
		out.WriteString("</td><td class=\"ttd\">")
		if outfmt.line2_start < outfmt.line2_end {
			fmt.Fprintf(out, "<span class=\"lin\">Line %d to %d</span>", outfmt.line2_start+1, outfmt.line2_end)
		}
		out.Write(outfmt.buf2.Bytes())
		out.WriteString("</td></tr>\n")
	}

	outfmt.buf1.Reset()
	outfmt.buf2.Reset()

	outfmt.line1_start = -1
	outfmt.line2_start = -1
}

// Add 'context' lines to diff output
func html_add_context_lines(outfmt *OutputFormat, data1, data2 [][]byte, line1, line2 int) {
	var end1, end2 int

	// Add 'context' lines after the last 'diff' and before this 'diff' segment
	if outfmt.line1_end > 0 || outfmt.line2_end > 0 {
		end1 = min_int(len(data1), outfmt.line1_end+flag_context_lines)
		end2 = min_int(len(data2), outfmt.line2_end+flag_context_lines)

		if end1 < line1 && end2 < line2 {
			outfmt.buf1.WriteString("<span class=\"nop\">")
			for _, line := range data1[outfmt.line1_end:end1] {
				write_html_bytes(&outfmt.buf1, line)
				outfmt.buf1.WriteByte('\n')
			}
			outfmt.line1_end = end1
			outfmt.buf1.WriteString("</span>")

			outfmt.buf2.WriteString("<span class=\"nop\">")
			for _, line := range data2[outfmt.line2_end:end2] {
				write_html_bytes(&outfmt.buf2, line)
				outfmt.buf2.WriteByte('\n')
			}
			outfmt.line2_end = end2
			outfmt.buf2.WriteString("</span>")
		}
	}

	if line1 >= len(data1) && line2 >= len(data2) {
		return
	}

	end1 = line1 - flag_context_lines
	end2 = line2 - flag_context_lines

	if end1 > 0 && end2 > 0 && end1 > outfmt.line1_end && end2 > outfmt.line2_end {

		html_add_block(outfmt)

		outfmt.line1_end = end1
		outfmt.line2_end = end2
	}

	if outfmt.line1_start < 0 {
		outfmt.line1_start = outfmt.line1_end
	}
	if outfmt.line2_start < 0 {
		outfmt.line2_start = outfmt.line2_end
	}

	if line1 > outfmt.line1_end {
		outfmt.buf1.WriteString("<span class=\"nop\">")
		for _, line := range data1[outfmt.line1_end:line1] {
			write_html_bytes(&outfmt.buf1, line)
			outfmt.buf1.WriteByte('\n')
		}
		outfmt.line1_end = line1
		outfmt.buf1.WriteString("</span>")
	}

	if line2 > outfmt.line2_end {
		outfmt.buf2.WriteString("<span class=\"nop\">")
		for _, line := range data2[outfmt.line2_end:line2] {
			write_html_bytes(&outfmt.buf2, line)
			outfmt.buf2.WriteByte('\n')
		}
		outfmt.line2_end = line2
		outfmt.buf2.WriteString("</span>")
	}
}

func (chg *DiffChangeFileHtml) diff_same(start1, end1, start2, end2 int) {
}

func (chg *DiffChangeFileHtml) diff_insert(start1, end1, start2, end2 int) {
	html_add_context_lines(chg.outfmt, chg.file1, chg.file2, start1, start2)
	chg.outfmt.buf2.WriteString("<span class=\"add\">")
	for _, line := range chg.file2[start2:end2] {
		write_html_bytes(&chg.outfmt.buf2, line)
		chg.outfmt.buf2.WriteByte('\n')
	}
	chg.outfmt.buf2.WriteString("</span>")
	chg.outfmt.line2_end = end2
}

func (chg *DiffChangeFileHtml) diff_remove(start1, end1, start2, end2 int) {
	html_add_context_lines(chg.outfmt, chg.file1, chg.file2, start1, start2)
	chg.outfmt.buf1.WriteString("<span class=\"del\">")
	for _, line := range chg.file1[start1:end1] {
		write_html_bytes(&chg.outfmt.buf1, line)
		chg.outfmt.buf1.WriteByte('\n')
	}
	chg.outfmt.buf1.WriteString("</span>")
	chg.outfmt.line1_end = end1
}

func (chg *DiffChangeFileHtml) diff_modify(start1, end1, start2, end2 int) {

	outfmt, data1, data2 := chg.outfmt, chg.file1, chg.file2
	html_add_context_lines(outfmt, data1, data2, start1, start2)

	outfmt.buf1.WriteString("<span class=\"upd\">")
	outfmt.buf2.WriteString("<span class=\"upd\">")

	for start1 < end1 && start2 < end2 {

		if flag_suppress_line_changes {
			write_html_bytes(&outfmt.buf1, data1[start1])
			write_html_bytes(&outfmt.buf2, data2[start2])
		} else {
			// report on changes within the line
			line1, line2 := data1[start1], data2[start2]
			pos1, cmp1 := split_runes(line1)
			pos2, cmp2 := split_runes(line2)

			change1, change2 := do_diff(cmp1, cmp2)

			if change1 != nil {

				// perform shift boundaries, to make the changes more readable
				shift_boundaries(cmp1, change1, rune_bouundary_score)
				shift_boundaries(cmp2, change2, rune_bouundary_score)

				chg := DiffChangeLine{
					outfmt: outfmt,
					line1:  line1,
					line2:  line2,
					pos1:   pos1,
					pos2:   pos2,
				}
				report_changes(&chg, cmp1, cmp2, change1, change2)
			}
		}

		outfmt.buf1.WriteByte('\n')
		outfmt.buf2.WriteByte('\n')
		start1++
		start2++
	}

	outfmt.buf1.WriteString("</span>")
	outfmt.buf2.WriteString("</span>")
	outfmt.line1_end = start1
	outfmt.line2_end = start2

	if start1 < end1 {
		outfmt.buf1.WriteString("<span class=\"del\">")
		for _, line := range data1[start1:end1] {
			write_html_bytes(&outfmt.buf1, line)
			outfmt.buf1.WriteByte('\n')
		}
		outfmt.buf1.WriteString("</span>")
		outfmt.line1_end = end1
	}

	if start2 < end2 {
		outfmt.buf2.WriteString("<span class=\"add\">")
		for _, line := range data2[start2:end2] {
			write_html_bytes(&outfmt.buf2, line)
			outfmt.buf2.WriteByte('\n')
		}
		outfmt.buf2.WriteString("</span>")
		outfmt.line2_end = end2
	}
}

func (chg *DiffChangeLine) diff_insert(start1, end1, start2, end2 int) {
	chg.outfmt.buf2.WriteString("<span class=\"chg\">")
	write_html_bytes(&chg.outfmt.buf2, chg.line2[chg.pos2[start2]:chg.pos2[end2]])
	chg.outfmt.buf2.WriteString("</span>")
}

func (chg *DiffChangeLine) diff_remove(start1, end1, start2, end2 int) {
	chg.outfmt.buf1.WriteString("<span class=\"chg\">")
	write_html_bytes(&chg.outfmt.buf1, chg.line1[chg.pos1[start1]:chg.pos1[end1]])
	chg.outfmt.buf1.WriteString("</span>")
}

func (chg *DiffChangeLine) diff_modify(start1, end1, start2, end2 int) {
	chg.diff_remove(start1, end1, start2, end2)
	chg.diff_insert(start1, end1, start2, end2)
}

func (chg *DiffChangeLine) diff_same(start1, end1, start2, end2 int) {
	write_html_bytes(&chg.outfmt.buf1, chg.line1[chg.pos1[start1]:chg.pos1[end1]])
	write_html_bytes(&chg.outfmt.buf2, chg.line2[chg.pos2[start2]:chg.pos2[end2]])
}

func diff_text_header(outfmt *OutputFormat) {
	if !outfmt.header_printed {
		out_acquire_lock()
		outfmt.header_printed = true
		fmt.Fprintf(out, "#< %s\n", outfmt.name1)
		fmt.Fprintf(out, "#> %s\n", outfmt.name2)
	}
}

func (chg *DiffChangeFileText) diff_same(start1, end1, start2, end2 int) {
}

func (chg *DiffChangeFileText) diff_modify(start1, end1, start2, end2 int) {
	diff_text_header(chg.outfmt)
	switch {
	case end1-start1 == 1 && end2-start2 == 1:
		fmt.Fprintf(out, "%dc%d\n", start1+1, start2+1)
	case end1-start1 == 1:
		fmt.Fprintf(out, "%dc%d,%d\n", start1+1, start2+1, end2)
	case end2-start2 == 1:
		fmt.Fprintf(out, "%d,%dc%d\n", start1+1, end1, start2+1)
	default:
		fmt.Fprintf(out, "%d,%dc%d,%d\n", start1+1, end1, start2+1, end2)
	}

	for _, line := range chg.file1[start1:end1] {
		out.WriteString("< ")
		out.Write(line)
		out.WriteByte('\n')
	}

	out.WriteString("---\n")

	for _, line := range chg.file2[start2:end2] {
		out.WriteString("> ")
		out.Write(line)
		out.WriteByte('\n')
	}
}

func (chg *DiffChangeFileText) diff_insert(start1, end1, start2, end2 int) {
	diff_text_header(chg.outfmt)
	if end2-start2 == 1 {
		fmt.Fprintf(out, "%da%d\n", start1, start2+1)
	} else {
		fmt.Fprintf(out, "%da%d,%d\n", start1, start2+1, end2)
	}

	for _, line := range chg.file2[start2:end2] {
		out.WriteString("> ")
		out.Write(line)
		out.WriteByte('\n')
	}
}

func (chg *DiffChangeFileText) diff_remove(start1, end1, start2, end2 int) {
	diff_text_header(chg.outfmt)
	if end1-start1 == 1 {
		fmt.Fprintf(out, "%dd%d\n", start1+1, start2)
	} else {
		fmt.Fprintf(out, "%d,%dd%d\n", start1+1, end1, start2)
	}

	for _, line := range chg.file1[start1:end1] {
		out.WriteString("< ")
		out.Write(line)
		out.WriteByte('\n')
	}
}

func is_space(b byte) bool {
	return b == ' ' || b == '\t' || b == '\v' || b == '\f'
}

func get_next_rune_nonspace(line []byte, i int) (rune, int) {
	b, size := utf8.DecodeRune(line[i:])
	i += size
	if !unicode.IsSpace(b) {
		return b, i
	}
	for i < len(line) {
		b, size := utf8.DecodeRune(line[i:])
		i += size
		if !unicode.IsSpace(b) {
			return b, i
		}
	}
	return 0, i
}

func get_next_rune_xspace(line []byte, i int) (rune, int) {
	b, size := utf8.DecodeRune(line[i:])
	i += size
	if !unicode.IsSpace(b) {
		return b, i
	}
	for i < len(line) {
		b, size := utf8.DecodeRune(line[i:])
		if !unicode.IsSpace(b) {
			return ' ', i
		}
		i += size
	}
	return ' ', i
}

func get_next_byte_nonspace(line []byte, i int) (byte, int) {
	b, i := line[i], i+1
	if !is_space(b) {
		return b, i
	}
	for i < len(line) {
		b, i = line[i], i+1
		if !is_space(b) {
			return b, i
		}
	}
	return 0, i
}

func get_next_byte_xspace(line []byte, i int) (byte, int) {
	b, i := line[i], i+1
	if !is_space(b) {
		return b, i
	}
	for i < len(line) {
		b = line[i]
		if !is_space(b) {
			return ' ', i
		}
		i = i + 1
	}
	return ' ', i
}

func compare_line_bytes(line1, line2 []byte) bool {
	len1, len2 := len(line1), len(line2)
	var i, j int
	var v1, v2 byte
	switch {
	case flag_cmp_ignore_all_space:
		for i < len1 && j < len2 {
			v1, i = get_next_byte_nonspace(line1, i)
			v2, j = get_next_byte_nonspace(line2, j)
			if flag_cmp_ignore_case && v1 != v2 {
				v1, v2 = to_lower_byte(v1), to_lower_byte(v2)
			}
			if v1 != v2 {
				return false
			}
		}
		if i < len1 || j < len2 {
			return false
		}

	case flag_cmp_ignore_space_change:
		for i < len1 && j < len2 {
			v1, i = get_next_byte_xspace(line1, i)
			v2, j = get_next_byte_xspace(line2, j)
			if flag_cmp_ignore_case && v1 != v2 {
				v1, v2 = to_lower_byte(v1), to_lower_byte(v2)
			}
			if v1 != v2 {
				return false
			}
		}
		if i < len1 || j < len2 {
			return false
		}

	case flag_cmp_ignore_case:
		if len1 != len2 {
			return false
		}
		for i < len1 && j < len2 {
			if to_lower_byte(line1[i]) != to_lower_byte(line2[j]) {
				return false
			}
			i, j = i+1, j+1
		}
		if i < len1 || j < len2 {
			return false
		}
	}
	return true
}

func compare_line_unicode(line1, line2 []byte) bool {
	len1, len2 := len(line1), len(line2)
	var i, j int
	var v1, v2 rune
	var size1, size2 int
	switch {
	case flag_cmp_ignore_all_space:
		for i < len1 && j < len2 {
			v1, i = get_next_rune_nonspace(line1, i)
			v2, j = get_next_rune_nonspace(line2, j)
			if flag_cmp_ignore_case && v1 != v2 {
				v1, v2 = unicode.ToLower(v1), unicode.ToLower(v2)
			}
			if v1 != v2 {
				return false
			}
		}
		if i < len1 || j < len2 {
			return false
		}

	case flag_cmp_ignore_space_change:
		for i < len1 && j < len2 {
			v1, i = get_next_rune_xspace(line1, i)
			v2, j = get_next_rune_xspace(line2, j)
			if flag_cmp_ignore_case && v1 != v2 {
				v1, v2 = unicode.ToLower(v1), unicode.ToLower(v2)
			}
			if v1 != v2 {
				return false
			}
		}
		if i < len1 || j < len2 {
			return false
		}

	case flag_cmp_ignore_case:
		if len1 != len2 {
			return false
		}
		for i < len1 && j < len2 {
			v1, size1 = utf8.DecodeRune(line1[i:])
			v2, size2 = utf8.DecodeRune(line2[j:])
			if v1 != v2 && unicode.ToLower(v1) != unicode.ToLower(v2) {
				return false
			}
			i, j = i+size1, j+size2
		}
		if i < len1 || j < len2 {
			return false
		}
	}
	return true
}

var crc_table = crc32.MakeTable(crc32.Castagnoli)

func hash32(h uint32, b byte) uint32 {
	return crc_table[byte(h)^b] ^ (h >> 8)
}

func compute_hash_exact(data []byte) uint32 {
	// On amd64, this will be using the SSE4.2 hardware instructions, much faster!
	return crc32.Update(0, crc_table, data)
}

func compute_hash_bytes(line1 []byte) uint32 {
	var hash uint32
	switch {
	case flag_cmp_ignore_all_space:
		for _, v1 := range line1 {
			if !is_space(v1) {
				if flag_cmp_ignore_case {
					v1 = to_lower_byte(v1)
				}
				hash = hash32(hash, v1)
			}
		}

	case flag_cmp_ignore_space_change:
		last_space := false
		for _, v1 := range line1 {
			if is_space(v1) {
				if last_space {
					continue
				}
				last_space = true
				v1 = ' '
			} else {
				last_space = false
				if flag_cmp_ignore_case {
					v1 = to_lower_byte(v1)
				}
			}
			hash = hash32(hash, v1)
		}

	case flag_cmp_ignore_case:
		for _, v1 := range line1 {
			v1 = to_lower_byte(v1)
			hash = hash32(hash, v1)
		}

	}
	return hash
}

func compute_hash_unicode(line1 []byte) uint32 {
	var hash uint32
	i, len1 := 0, len(line1)

	switch {
	case flag_cmp_ignore_all_space:
		for i < len1 {
			v1, size := utf8.DecodeRune(line1[i:])
			i = i + size
			if !unicode.IsSpace(v1) {
				if flag_cmp_ignore_case {
					v1 = unicode.ToLower(v1)
				}
				for v1 != 0 {
					hash = hash32(hash, byte(v1))
					v1 = v1 >> 8
				}
			}
		}

	case flag_cmp_ignore_space_change:
		for i < len1 {
			v1, size := utf8.DecodeRune(line1[i:])
			i = i + size
			if unicode.IsSpace(v1) {
				for i < len1 {
					v2, size := utf8.DecodeRune(line1[i:])
					if !unicode.IsSpace(v2) {
						break
					}
					i += size
				}
				v1 = ' '
			}
			if flag_cmp_ignore_case {
				v1 = unicode.ToLower(v1)
			}
			for v1 != 0 {
				hash = hash32(hash, byte(v1))
				v1 = v1 >> 8
			}
		}

	case flag_cmp_ignore_case:
		for i < len1 {
			v1, size := utf8.DecodeRune(line1[i:])
			i = i + size
			v1 = unicode.ToLower(v1)
			for v1 != 0 {
				hash = hash32(hash, byte(v1))
				v1 = v1 >> 8
			}
		}
	}
	return hash
}

type EquivClass struct {
	id   int
	hash uint32
	line *[]byte
	next *EquivClass
}

type LinesData struct {
	ids        []int // Id's for each line,
	zids       []int // list of ids with unmatched lines replaced by a single entry (and blank lines removed)
	zcount     []int // Number of lines that represent each zids entry
	change     []bool
	zids_start int
	zids_end   int
}

//
// Compute id's that represent the original lines, these numeric id's are use for faster line comparison.
//
func find_equiv_lines(lines1, lines2 [][]byte) (*LinesData, *LinesData) {

	info1 := LinesData{
		ids:    make([]int, len(lines1)),
		change: make([]bool, len(lines1)),
	}

	info2 := LinesData{
		ids:    make([]int, len(lines2)),
		change: make([]bool, len(lines2)),
	}

	// since we already have a hashing function, it's faster to use arrays than to use go's builtin map
	// Use bucket size that is power of 2
	buckets := 1 << 9
	for buckets < (len(lines1)+len(lines2))*2 {
		buckets = buckets << 1
	}

	// create the slice we are using for hash tables
	eqhash := make([]*EquivClass, buckets)

	// Use id=0 for blank lines.
	// Later in report_changes(), do not report changes on chunks of lines with id=0
	if flag_cmp_ignore_blank_lines {
		hashcode := compute_hash(blank_line)
		ihash := int(hashcode) & (buckets - 1)
		eqhash[ihash] = &EquivClass{id: 0, line: &blank_line, hash: hashcode}
	}

	// the unique id for identical lines, start with 1.
	var max_id_f1, max_id_f2 int
	next_id := 1

	// process both sets of lines
	for findex := 0; findex < 2; findex++ {
		var lines [][]byte
		var ids []int

		if findex == 0 {
			lines = lines1
			ids = info1.ids
		} else {
			lines = lines2
			ids = info2.ids
		}

		for i := 0; i < len(lines); i++ {
			lptr := &lines[i]
			// find current line in eqhash
			hashcode := compute_hash(*lptr)
			ihash := int(hashcode) & (buckets - 1)
			eq := eqhash[ihash]

			if eq == nil {
				// not found in eqhash, create new entry
				ids[i] = next_id
				eqhash[ihash] = &EquivClass{id: next_id, line: lptr, hash: hashcode}
				next_id++
			} else if eq.hash == hashcode && compare_line(*lptr, *eq.line) {
				// found, and line is the same. reuse same id
				ids[i] = eq.id
			} else {
				// hash-collision. look through link-list for same match
				n := eq.next
				for n != nil {
					if n.hash == hashcode && compare_line(*lptr, *n.line) {
						ids[i] = n.id
						break
					}
					n = n.next
				}
				// new entry, link to start of linked-list
				if n == nil {
					ids[i] = next_id
					eq.next = &EquivClass{id: next_id, line: lptr, hash: hashcode, next: eq.next}
					next_id++
				}
			}
		}

		if findex == 0 {
			max_id_f1 = next_id - 1
		} else {
			max_id_f2 = next_id - 1
		}
	}

	compress_equiv_ids(&info1, &info2, max_id_f1, max_id_f2)

	return &info1, &info2
}

// Count the occurrances of each unique ids in both sets of lines, we will then know which lines are only present in one file, but not the other.
// Remove chunks of lines that do not appear in the other files, and replace with a single entry
// Return compressed lists of ids and a list indicating where are the chunk of lines being replaced
func compress_equiv_ids(lines1, lines2 *LinesData, max_id1, max_id2 int) {

	len1, len2 := len(lines1.ids), len(lines2.ids)
	has_ids1, has_ids2 := make([]bool, max_id1+1), make([]bool, max_id2+1)

	// Determine which id's are in the each file
	for _, v := range lines1.ids {
		has_ids1[v] = true
	}
	for _, v := range lines2.ids {
		has_ids2[v] = true
	}

	// exclude lines from the begining that are identical in both files
	// if line in file1 but not in file2, exclude it and marked as changed
	// if line in file2 but not in file1, exclude it and marked as changed
	i1, i2 := 0, 0
	for i1 < len1 && i2 < len2 {
		v1, v2 := lines1.ids[i1], lines2.ids[i2]
		if v1 > max_id2 || !has_ids2[v1] {
			lines1.change[i1] = true
			i1++
		} else if v2 > max_id1 || !has_ids1[v2] {
			lines2.change[i2] = true
			i2++
		} else if v1 == v2 {
			i1++
			i2++
		} else {
			break
		}
	}

	// exclude lines from the end that are identical in both files
	// if line in file1 but not in file2, exclude it and marked as changed
	// if line in file2 but not in file1, exclude it and marked as changed
	j1, j2 := len1, len2
	for i1 < j1 && i2 < j2 {
		v1, v2 := lines1.ids[j1-1], lines2.ids[j2-1]
		if v1 > max_id2 || !has_ids2[v1] {
			j1--
			lines1.change[j1] = true
		} else if v2 > max_id1 || !has_ids1[v2] {
			j2--
			lines2.change[j2] = true
		} else if v1 == v2 {
			j1--
			j2--
		} else {
			break
		}
	}

	// One of the list is now empty, no need to run diff algorithm for comparison.
	// Just mark the remaining lines other list as changed.
	if i1 == j1 {
		for i2 < j2 {
			lines2.change[i2] = true
			i2++
		}
		return
	}
	if i2 == j2 {
		for i1 < j1 {
			lines1.change[i1] = true
			i1++
		}
		return
	}

	// store excluded lines from begining and end of file
	lines1.zids_start, lines1.zids_end = i1, j1
	lines2.zids_start, lines2.zids_end = i2, j2

	// Go through all lines, replace chunk of lines that does not exists in the
	// other set with a single entry and a negative new id).
	next_id := max_int(max_id1, max_id2) + 1
	for findex := 0; findex < 2; findex++ {
		var ids []int
		var has_ids []bool
		var max_id int

		if findex == 0 {
			ids = lines1.ids[lines1.zids_start:lines1.zids_end]
			has_ids = has_ids2
			max_id = max_id2
		} else {
			ids = lines2.ids[lines2.zids_start:lines2.zids_end]
			has_ids = has_ids1
			max_id = max_id1
		}

		// new slices for compressed ids and the number of lines each entry replaced
		// use a new negative id for those merged lines
		zcount := make([]int, len(ids))
		zids := make([]int, len(ids))

		lastexclude := false
		n := 0
		for _, v := range ids {
			exclude := (v > max_id || !has_ids[v])
			if exclude && lastexclude {
				zcount[n-1]++
				zids[n-1] = -next_id
				next_id++
			} else if exclude {
				zcount[n]++
				zids[n] = -v
				n++
			} else {
				zcount[n]++
				zids[n] = v
				n++
			}
			lastexclude = exclude
		}

		// shrink the slice
		zids = zids[:n]
		zcount = zcount[:n]

		if findex == 0 {
			lines1.zids = zids
			lines1.zcount = zcount
		} else {
			lines2.zids = zids
			lines2.zcount = zcount
		}
	}
}

//
// Do the reverse of the compress_equiv_ids.
// zllines1 and zlines2 contains the 'extra' lines each entry represents.
//
func expand_change_list(info1, info2 *LinesData, zchange1, zchange2 []bool) {

	for findex := 0; findex < 2; findex++ {
		var info *LinesData
		var change, zchange []bool

		// expand the changes into the range between zids_start and zids_end
		if findex == 0 {
			info = info1
			change = info1.change[info1.zids_start:]
			zchange = zchange1
		} else {
			info = info2
			change = info2.change[info2.zids_start:]
			zchange = zchange2
		}

		// no change
		if zchange == nil {
			continue
		}

		// expand each entry by the number of lines in zcount[]
		n := 0
		for i, m := range info.zcount {
			if zchange[i] {
				for end := n + m; n < end; n++ {
					change[n] = true
				}
			} else {
				n += m
			}
		}
	}
}

// open file, and read/mmap the entire content into byte array
func open_file(fname string, finfo os.FileInfo) *Filedata {

	file := &Filedata{name: fname, info: finfo}
	fsize := file.info.Size()

	var err error

	if fsize >= 1e8 {
		file.errormsg = MSG_FILE_TOO_BIG
		return file
	}

	// zero size file.
	if fsize <= 0 {
		return file
	}

	// open the file
	file.osfile, err = os.Open(file.name)
	if err != nil {
		file.osfile = nil
		file.errormsg = err.Error()
		return file
	}

	if fsize > MMAP_THRESHOLD {
		// map to file into memory, leave file open.
		file.data, err = map_file(file.osfile, 0, int(fsize))
		if err != nil {
			file.osfile.Close()
			file.osfile = nil
			file.errormsg = err.Error()
			return file
		}
		file.is_mapped = true
	} else {
		// read in the entire file
		fdata := make([]byte, fsize, fsize)
		n, err := file.osfile.Read(fdata)
		if err != nil {
			file.errormsg = err.Error()
			return file
		}
		file.data = fdata[:n]
		// close file
		file.osfile.Close()
		file.osfile = nil
	}

	return file
}

// Close file (and umap it)
func (file *Filedata) close_file() {
	if file.osfile != nil {
		if file.is_mapped && file.data != nil {
			unmap_file(file.data)
		}
		file.osfile.Close()
		file.osfile = nil
	}
	file.data = nil
}

// check if file is binary
func (file *Filedata) check_binary() {
	if file.data == nil {
		return
	}
	if len(file.data) == 0 {
		file.data = nil
		file.errormsg = MSG_FILE_SIZE_ZERO
		return
	}
	if bytes.IndexByte(file.data[0:min_int(len(file.data), BINARY_CHECK_SIZE)], 0) >= 0 {
		file.data = nil
		file.errormsg = MSG_FILE_IS_BINARY
		return
	}
}

//
// split up data into text lines
//
func (file *Filedata) split_lines() [][]byte {

	lines := make([][]byte, 0, min_int(len(file.data)/32, 500))
	var i, previ int
	var b, lastb byte

	data := file.data
	for i, b = range data {
		// accept dos, unix, mac newline
		if b == '\n' && lastb == '\r' {
			previ = i + 1
		} else if b == '\n' || b == '\r' {
			lines = append(lines, data[previ:i])
			previ = i + 1
		} else if b == 0 && i < BINARY_CHECK_SIZE {
			file.is_binary = true
			file.errormsg = MSG_FILE_IS_BINARY
			return nil
		}
		lastb = b
	}

	// add last incomplete line (if required)
	if len(data) > previ {
		lines = append(lines, data[previ:len(data)])
	}

	return lines
}

// for sorting os.FileInfo by name
type FileInfoList []os.FileInfo

func (s FileInfoList) Len() int           { return len(s) }
func (s FileInfoList) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s FileInfoList) Less(i, j int) bool { return s[i].Name() < s[j].Name() }

// get a list of sorted directory entries
func read_sorted_dir(dirname string) ([]os.FileInfo, error) {

	dir, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}

	all, err := dir.Readdir(-1)
	if err != nil {
		dir.Close()
		return nil, err
	}

	dir.Close()

	sort.Sort(FileInfoList(all))

	return all, nil
}

// compare 2 dirs.
func diff_dirs(dirname1, dirname2 string, finfo1, finfo2 os.FileInfo) {

	dirname1 = strings.TrimRight(dirname1, PATH_SEPARATOR)
	dirname2 = strings.TrimRight(dirname2, PATH_SEPARATOR)

	dir1, err1 := read_sorted_dir(dirname1)
	dir2, err2 := read_sorted_dir(dirname2)

	if err1 != nil || err2 != nil {
		msg1, msg2 := "", ""
		if err1 != nil {
			msg1 = err1.Error()
		}
		if err2 != nil {
			msg2 = err2.Error()
		}
		output_diff_message(dirname1, dirname2, finfo1, finfo2, msg1, msg2, true)
		return
	}

	// Loop through all files, then all directories
	for _, dir_mode := range []bool{false, true} {
		i1, i2 := 0, 0
		for i1 < len(dir1) || i2 < len(dir2) {
			name1, name2 := "", ""
			if i1 < len(dir1) {
				name1 = dir1[i1].Name()
				if dir1[i1].IsDir() != dir_mode || strings.HasPrefix(name1, ".") {
					i1++
					continue
				}
			}
			if i2 < len(dir2) {
				name2 = dir2[i2].Name()
				if dir2[i2].IsDir() != dir_mode || strings.HasPrefix(name2, ".") {
					i2++
					continue
				}
			}

			if name1 == name2 {
				if dir1[i1].IsDir() != dir2[i2].IsDir() {
					if !dir_mode {
						if dir1[i1].IsDir() {
							output_diff_message(dirname1+PATH_SEPARATOR+name1, dirname2+PATH_SEPARATOR+name2, dir1[i1], dir2[i2], MSG_THIS_IS_DIR, MSG_THIS_IS_FILE, true)
						} else {
							output_diff_message(dirname1+PATH_SEPARATOR+name1, dirname2+PATH_SEPARATOR+name2, dir1[i1], dir2[i2], MSG_THIS_IS_FILE, MSG_THIS_IS_DIR, true)
						}
					}
				} else if dir_mode {
					// compare sub-directories
					diff_dirs(dirname1+PATH_SEPARATOR+name1, dirname2+PATH_SEPARATOR+name2, dir1[i1], dir2[i2])
				} else {
					// compare files
					if flag_max_goroutines > 1 {
						queue_diff_file(dirname1+PATH_SEPARATOR+name1, dirname2+PATH_SEPARATOR+name2, dir1[i1], dir2[i2])
					} else {
						diff_file(dirname1+PATH_SEPARATOR+name1, dirname2+PATH_SEPARATOR+name2, dir1[i1], dir2[i2])
					}
				}
				i1, i2 = i1+1, i2+1
			} else if (i1 < len(dir1) && name1 < name2) || i2 >= len(dir2) {
				if dir_mode {
					output_diff_message(dirname1+PATH_SEPARATOR+name1, dirname2+PATH_SEPARATOR+name1, dir1[i1], nil, "", MSG_DIR_NOT_EXISTS, true)
				} else {
					if flag_suppress_missing_file {
						output_diff_message(dirname1+PATH_SEPARATOR+name1, dirname2+PATH_SEPARATOR+name1, dir1[i1], nil, "", MSG_FILE_NOT_EXISTS, true)
					} else {
						fdata := open_file(dirname1+PATH_SEPARATOR+name1, dir1[i1])
						fdata.check_binary()
						output_diff_message_content(dirname1+PATH_SEPARATOR+name1, dirname2+PATH_SEPARATOR+name1, dir1[i1], nil, fdata.errormsg, MSG_FILE_NOT_EXISTS, fdata.data, nil, true)
						fdata.close_file()
					}
				}
				i1++
			} else if (i2 < len(dir2) && name2 < name1) || i1 >= len(dir1) {
				if dir_mode {
					output_diff_message(dirname1+PATH_SEPARATOR+name2, dirname2+PATH_SEPARATOR+name2, nil, dir2[i2], MSG_DIR_NOT_EXISTS, "", true)
				} else {
					if flag_suppress_missing_file {
						output_diff_message(dirname1+PATH_SEPARATOR+name2, dirname2+PATH_SEPARATOR+name2, nil, dir2[i2], MSG_FILE_NOT_EXISTS, "", true)
					} else {
						fdata := open_file(dirname2+PATH_SEPARATOR+name2, dir2[i2])
						fdata.check_binary()
						output_diff_message_content(dirname1+PATH_SEPARATOR+name2, dirname2+PATH_SEPARATOR+name2, nil, dir2[i2], MSG_FILE_NOT_EXISTS, fdata.errormsg, nil, fdata.data, true)
						fdata.close_file()
					}
				}
				i2++
			} else {
				break
			}
		}
	}
}

// compare 2 file
func diff_file(filename1, filename2 string, finfo1, finfo2 os.FileInfo) {

	file1 := open_file(filename1, finfo1)
	file2 := open_file(filename2, finfo2)

	defer file1.close_file()
	defer file2.close_file()

	if file1.errormsg != "" || file2.errormsg != "" {
		// display error messages
		output_diff_message(filename1, filename2, finfo1, finfo2, file1.errormsg, file2.errormsg, true)
		return
	} else if bytes.Equal(file1.data, file2.data) {
		// files are equal
		if flag_show_identical_files {
			output_diff_message(filename1, filename2, finfo1, finfo2, MSG_FILE_IDENTICAL, MSG_FILE_IDENTICAL, false)
		}
		return
	}

	lines1 := file1.split_lines()
	lines2 := file2.split_lines()

	if file1.is_binary || file2.is_binary {

		var msg1, msg2 string

		if file1.is_binary {
			msg1 = MSG_BIN_FILE_DIFFERS
		} else {
			msg1 = MSG_FILE_DIFFERS
		}

		if file2.is_binary {
			msg2 = MSG_BIN_FILE_DIFFERS
		} else {
			msg2 = MSG_FILE_DIFFERS
		}

		if msg1 != "" || msg2 != "" {
			output_diff_message(filename1, filename2, finfo1, finfo2, msg1, msg2, true)
		}
	} else {
		// Compute equiv ids for each line.
		info1, info2 := find_equiv_lines(lines1, lines2)

		// No zids avaiable, no need to run diff comparision algorithm
		// The find_equiv_lines() function may have perform the comparison already.
		if info1.zids != nil && info2.zids != nil {
			// run the diff algorithm
			zchange1, zchange2 := do_diff(info1.zids, info2.zids)

			// expand the change list, so that change array contains changes to actual lines
			expand_change_list(info1, info2, zchange1, zchange2)
		}

		// perform shift boundary
		shift_boundaries(info1.ids, info1.change, nil)
		shift_boundaries(info2.ids, info2.change, nil)

		outfmt := OutputFormat{
			name1:     filename1,
			name2:     filename2,
			fileinfo1: finfo1,
			fileinfo2: finfo2,
		}

		var chg DiffChanger

		// Choose change output format: text or html
		if flag_output_as_text {
			chg = &DiffChangeFileText{
				outfmt: &outfmt,
				file1:  lines1,
				file2:  lines2,
			}
		} else {
			chg = &DiffChangeFileHtml{
				outfmt: &outfmt,
				file1:  lines1,
				file2:  lines2,
			}
		}

		// output diff results
		changed := report_changes(chg, info1.ids, info2.ids, info1.change, info2.change)

		if flag_output_as_text {
			if outfmt.header_printed {
				outfmt.header_printed = false
				out_release_lock()
			}
		} else {
			// output remaining lines of all files
			html_add_context_lines(&outfmt, lines1, lines2, len(lines1), len(lines2))
			html_add_block(&outfmt)

			if outfmt.header_printed {
				out.WriteString("</table><br>\n")
				outfmt.header_printed = false
				out_release_lock()
			}
		}

		if !changed && flag_show_identical_files {
			// report on identical file if required
			output_diff_message(filename1, filename2, finfo1, finfo2, MSG_FILE_IDENTICAL, MSG_FILE_IDENTICAL, false)
		}
	}
}

// shortcut functions. hopefully will be inlined by compiler
func max_int(a, b int) int {
	if a < b {
		return b
	}
	return a
}

// shortcut functions. hopefully will be inlined by compiler
func min_int(a, b int) int {
	if a < b {
		return a
	}
	return b
}

//
// An O(ND) Difference Algorithm: Find middle snake
//
func algorithm_sms(data1, data2 []int, v []int) (int, int, int, int) {

	end1, end2 := len(data1), len(data2)
	max := end1 + end2 + 1
	up_k := end1 - end2
	odd := (up_k & 1) != 0
	down_off, up_off := max, max-up_k+max+max+2

	v[down_off+1] = 0
	v[down_off] = 0
	v[up_off+up_k-1] = end1
	v[up_off+up_k] = end1

	var k, x, u, z int

	for d := 1; true; d++ {
		up_k_plus_d := up_k + d
		up_k_minus_d := up_k - d
		for k = -d; k <= d; k += 2 {
			x = v[down_off+k+1]
			if k > -d && (k == d || z >= x) {
				x, z = z+1, x
			} else {
				z = x
			}
			for u = x; x < end1 && x-k < end2 && data1[x] == data2[x-k]; x++ {
			}
			if odd && (up_k_minus_d < k) && (k < up_k_plus_d) && v[up_off+k] <= x {
				return u, u - k, x, x - k
			}
			v[down_off+k] = x
		}
		z = v[up_off+up_k_minus_d-1]
		for k = up_k_minus_d; k <= up_k_plus_d; k += 2 {
			x = z
			if k < up_k_plus_d {
				z = v[up_off+k+1]
				if k == up_k_minus_d || z <= x {
					x = z - 1
				}
			}
			for u = x; x > 0 && x > k && data1[x-1] == data2[x-k-1]; x-- {
			}
			if !odd && (-d <= k) && (k <= d) && x <= v[down_off+k] {
				return x, x - k, u, u - k
			}
			v[up_off+k] = x
		}
	}
	return 0, 0, 0, 0 // should not reach here
}

//
// Special case for algorithm_sms() with only 1 item.
//
func find_one_sms(value int, list []int) (int, int) {
	for i, v := range list {
		if v == value {
			return 0, i
		}
	}
	return 1, 0
}

//
// An O(ND) Difference Algorithm: Find LCS
//
func algorithm_lcs(data1, data2 []int, change1, change2 []bool, v []int) {

	start1, start2 := 0, 0
	end1, end2 := len(data1), len(data2)

	// matches found at start and end of list
	for start1 < end1 && start2 < end2 && data1[start1] == data2[start2] {
		start1++
		start2++
	}
	for start1 < end1 && start2 < end2 && data1[end1-1] == data2[end2-1] {
		end1--
		end2--
	}

	len1, len2 := end1-start1, end2-start2

	switch {
	case len1 == 0:
		for start2 < end2 {
			change2[start2] = true
			start2++
		}

	case len2 == 0:
		for start1 < end1 {
			change1[start1] = true
			start1++
		}

	case len1 == 1 && len2 == 1:
		change1[start1] = true
		change2[start2] = true

	default:
		data1, change1 = data1[start1:end1], change1[start1:end1]
		data2, change2 = data2[start2:end2], change2[start2:end2]

		var x0, y0, x1, y1 int

		if len(data1) == 1 {
			// match one item, use simple search function
			x0, y0 = find_one_sms(data1[0], data2)
			x1, y1 = x0, y0
		} else if len(data2) == 1 {
			// match one item, use simple search function
			y0, x0 = find_one_sms(data2[0], data1)
			x1, y1 = x0, y0
		} else {
			// Find a point with the longest common sequence
			x0, y0, x1, y1 = algorithm_sms(data1, data2, v)
		}

		// Use the partitions to split this problem into subproblems.
		algorithm_lcs(data1[:x0], data2[:y0], change1[:x0], change2[:y0], v)
		algorithm_lcs(data1[x1:], data2[y1:], change1[x1:], change2[y1:], v)
	}
}

// Perform the shift
func do_shift_boundary(start, end, offset int, change []bool) {
	if offset < 0 {
		for offset != 0 {
			start, end, offset = start-1, end-1, offset+1
			change[start], change[end] = true, false
		}
	} else {
		for offset != 0 {
			change[start], change[end] = false, true
			start, end, offset = start+1, end+1, offset-1
		}
	}
}

// Determine if the changes starting at 'pos' can be shifted 'up' or 'down'
func find_shift_boundary(start int, data []int, change []bool) (int, int, int, bool, bool) {
	end, dlen := start+1, len(data)
	up, down := 0, 0

	// Find the end of this chunk of changes
	for end < dlen && change[end] {
		end++
	}

	for start-up-1 >= 0 && !change[start-up-1] && data[start-up-1] == data[end-up-1] {
		up = up + 1
	}

	for end+down < dlen && !change[end+down] && data[end+down] == data[start+down] {
		down = down + 1
	}

	// has changes been shifted to start/end of list or merged with previous/next change
	up_merge := (start-up == 0) || change[start-up-1]
	down_merge := (end+down == dlen) || change[end+down]

	return end, up, down, up_merge, down_merge
}

// scoring function for shifting characters in a line.
func rune_edge_score(r rune) int {

	switch r {
	case ' ', '\t', '\v', '\f':
		return 100

	case '<', '>', '(', ')', '[', ']', '\'', '"':
		return 40
	}

	return 0
}

// scoring character boundary, for finding a change chunk that is easier to read
func rune_bouundary_score(r1, r2 int) int {

	s1 := rune_edge_score(rune(r1))
	s2 := rune_edge_score(rune(r2))

	return s1 + s2
}

//
// shift changes up or down to make it more readable.
//
func shift_boundaries(data []int, change []bool, boundary_score func(int, int) int) {

	start, clen := 0, len(change)

	for start < clen {
		// find the next chunk of changes
		for start < clen && !change[start] {
			start++
		}
		if start >= clen {
			break
		}

		// find the limit of where this set of changes can be shifted
		end, up, down, up_merge, down_merge := find_shift_boundary(start, data, change)

		switch {
		case up > 0 && up_merge:
			// shift up, merged with previous chunk of changes
			do_shift_boundary(start, end, -up, change)
			// restart at the begining of this merged chunk
			for start -= up; start-1 >= 0 && change[start-1]; start-- {
			}

		case down > 0 && down_merge:
			// shift down, merged with next chunk of changes
			do_shift_boundary(start, end, down, change)
			start += down

		case (up > 0 || down > 0) && boundary_score != nil:
			// Only perform shifts when there is a boundary score function
			offset, best_score := 0, boundary_score(data[start], data[end-1])
			for i := -up; i <= down; i++ {
				if i != 0 {
					score := boundary_score(data[start+i], data[end+i-1])
					if score > best_score {
						offset, best_score = i, score
					}
				}
			}
			if offset != 0 {
				do_shift_boundary(start, end, offset, change)
			}
			start = end
			if offset > 0 {
				start += offset
			}

		default:
			// no shift
			start = end
		}
	}
}

// Wait for all jobs to finish
func job_queue_finish() {
	if flag_max_goroutines > 1 {
		job_wait.Wait()
	}
}

// Initialise job queues
func job_queue_init() {

	if flag_max_goroutines > 1 {

		if flag_max_goroutines > runtime.GOMAXPROCS(-1) {
			runtime.GOMAXPROCS(flag_max_goroutines)
		}

		// create async job queue channel
		job_queue = make(chan JobQueue, 1)

		// start up goroutines, to handle file comparison
		for i := 0; i < flag_max_goroutines; i++ {
			go func() {
				for job := range job_queue {
					diff_file(job.name1, job.name2, job.info1, job.info2)
					job_wait.Done()
				}
			}()
		}
	}
}

// Queue file comparison task
func queue_diff_file(fname1, fname2 string, finfo1, finfo2 os.FileInfo) {
	job_wait.Add(1)
	job_queue <- JobQueue{
		name1: fname1,
		name2: fname2,
		info1: finfo1,
		info2: finfo2,
	}
}

// Acquire Mutext lock on output stream
func out_acquire_lock() {
	if flag_max_goroutines > 1 {
		out_lock.Lock()
	}
}

// Release Mutext lock on output stream
func out_release_lock() {
	if flag_max_goroutines > 1 {
		out_lock.Unlock()
	}
}
