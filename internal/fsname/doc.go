// Package fsname checks that the file the OS opened for a path is the one
// the path names. On Windows an 8.3 short name (SETTIN~1.JSO) opens another
// file, so a check made on the requested name would judge the wrong file.
package fsname
