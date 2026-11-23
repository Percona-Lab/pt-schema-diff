package main

// Percona Toolkit style DSNs: h=host,P=3306,u=user,p=pass,S=/path/to/socket,D=database,A=charset,F=defaults-file.

import (
	"bufio"
	"crypto/tls"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

type dsn struct {
	Host, Port, User, Password, Socket, Database, Charset, DefaultsFile string
	passwordSet                                                         bool
}

var reDSN = regexp.MustCompile(`^[hPupSDAFt]=`)

func looksLikeDSN(s string) bool { return reDSN.MatchString(s) }

func parseDSN(s string) (dsn, error) {
	var d dsn
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok || len(k) != 1 {
			return d, fmt.Errorf("invalid DSN part %q in %q (expected key=value, e.g. h=host,P=3306,u=user,D=db)", part, s)
		}
		switch k {
		case "h":
			d.Host = v
		case "P":
			d.Port = v
		case "u":
			d.User = v
		case "p":
			d.Password, d.passwordSet = v, true
		case "S":
			d.Socket = v
		case "D":
			d.Database = v
		case "A":
			d.Charset = v
		case "F":
			d.DefaultsFile = v
		case "t":
			return d, fmt.Errorf("DSN key t (table) is not supported; use --tables")
		default:
			return d, fmt.Errorf("unknown DSN key %q in %q", k, s)
		}
	}
	return d, nil
}

// fill sets the values d leaves out from defaults (an earlier DSN, then command-line options).
func (d *dsn) fill(def dsn) {
	if d.Host == "" && d.Socket == "" {
		d.Host, d.Socket = def.Host, def.Socket
	}
	if d.Port == "" {
		d.Port = def.Port
	}
	if d.User == "" {
		d.User = def.User
	}
	if !d.passwordSet && def.passwordSet {
		d.Password, d.passwordSet = def.Password, true
	}
	if d.Charset == "" {
		d.Charset = def.Charset
	}
	if d.DefaultsFile == "" {
		d.DefaultsFile = def.DefaultsFile
	}
}

// readDefaultsFile reads user, password, host, port and socket from the [client] section of a my.cnf file.
func readDefaultsFile(path string) (dsn, error) {
	var d dsn
	f, err := os.Open(path)
	if err != nil {
		return d, err
	}
	defer f.Close()
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[] "))
			continue
		}
		if section != "client" {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		k = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(k)), "-", "_")
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "user":
			d.User = v
		case "password":
			d.Password, d.passwordSet = v, true
		case "host":
			d.Host = v
		case "port":
			d.Port = v
		case "socket":
			d.Socket = v
		}
	}
	return d, sc.Err()
}

func (d dsn) label() string {
	server := d.Socket
	if server == "" {
		server = orStr(d.Host, "localhost") + ":" + orStr(d.Port, "3306")
	}
	if d.Database != "" {
		return server + " · " + d.Database
	}
	return server
}

func orStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// open connects to the server of a DSN (without selecting its database).
func (d dsn) open(sslMode string) (*sql.DB, error) {
	if d.DefaultsFile != "" {
		def, err := readDefaultsFile(d.DefaultsFile)
		if err != nil {
			return nil, fmt.Errorf("defaults file: %w", err)
		}
		d.fill(def)
	}
	cfg := mysql.NewConfig()
	cfg.User = d.User
	cfg.Passwd = d.Password
	if !d.passwordSet {
		cfg.Passwd = os.Getenv("MYSQL_PWD")
	}
	if d.Socket != "" {
		cfg.Net, cfg.Addr = "unix", d.Socket
	} else {
		cfg.Net, cfg.Addr = "tcp", orStr(d.Host, "localhost")+":"+orStr(d.Port, "3306")
	}
	if d.Charset != "" {
		cfg.Params = map[string]string{"charset": d.Charset}
	}
	cfg.Timeout = 10 * time.Second
	cfg.AllowNativePasswords = true
	cfg.AllowCleartextPasswords = false
	switch strings.ToLower(sslMode) {
	case "", "preferred":
		cfg.TLSConfig = "preferred"
	case "disabled":
		cfg.TLSConfig = "false"
	case "required":
		cfg.TLSConfig = "skip-verify"
	case "verify_identity", "verify-identity":
		cfg.TLS = &tls.Config{ServerName: orStr(d.Host, "localhost"), MinVersion: tls.VersionTLS12}
	default:
		return nil, fmt.Errorf("invalid --ssl-mode %q (disabled, preferred, required, verify_identity)", sslMode)
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(2)
	return db, nil
}

// askPass reads a password from the terminal without echoing it.
func askPass(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("--ask-pass needs a terminal: %w", err)
	}
	defer tty.Close()
	fmt.Fprint(tty, prompt)
	off := exec.Command("stty", "-echo")
	off.Stdin = tty
	_ = off.Run()
	defer func() {
		on := exec.Command("stty", "echo")
		on.Stdin = tty
		_ = on.Run()
		fmt.Fprintln(tty)
	}()
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
