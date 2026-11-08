package api

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nor24o/lorahub/internal/model"
)

const remoteTimeout = 10 * time.Second

var remotePath = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

func (s *Server) remoteKeyPath() string { return filepath.Join(s.Cfg.DataDir, "remote_id_rsa") }

// remoteSigner loads the key used to log in to satellites, creating it once.
func (s *Server) remoteSigner() (ssh.Signer, error) {
	path := s.remoteKeyPath()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		k, gerr := rsa.GenerateKey(rand.Reader, 3072)
		if gerr != nil {
			return nil, gerr
		}
		b = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
		if err = os.WriteFile(path, b, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return nil, err
	}
	as, ok := signer.(ssh.AlgorithmSigner)
	if !ok {
		return signer, nil
	}
	// Dropbear on older OpenWrt only verifies SHA-1 RSA signatures.
	return ssh.NewSignerWithAlgorithms(as, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA})
}

func (s *Server) remoteKey(w http.ResponseWriter, r *http.Request) {
	signer, err := s.remoteSigner()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"public_key": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " lorahub"})
}

func remoteOf(g *model.Gateway) model.GatewayRemote {
	r := model.GatewayRemote{Port: 22, User: "root", ConfigPath: "/etc/lora/config.json"}
	if g.Remote != nil {
		r = *g.Remote
	}
	if r.Host == "" {
		if h, _, err := net.SplitHostPort(g.LastAddr); err == nil {
			r.Host = h
		} else {
			r.Host = g.LastAddr
		}
	}
	return r
}

type remoteSession struct {
	client *ssh.Client
}

func (s *Server) dialRemote(g *model.Gateway) (*remoteSession, model.GatewayRemote, error) {
	rc := remoteOf(g)
	if rc.Host == "" {
		return nil, rc, errors.New("set the gateway address first")
	}
	signer, err := s.remoteSigner()
	if err != nil {
		return nil, rc, err
	}
	var seen string
	cfg := &ssh.ClientConfig{User: rc.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, Timeout: remoteTimeout,
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			seen = ssh.FingerprintSHA256(key)
			if rc.HostKey != "" && rc.HostKey != seen {
				return fmt.Errorf("the gateway's SSH host key changed (expected %s, got %s); if that is expected, trust the new key in the connection settings", rc.HostKey, seen)
			}
			return nil
		}}
	c, err := ssh.Dial("tcp", net.JoinHostPort(rc.Host, fmt.Sprint(rc.Port)), cfg)
	if err != nil {
		return nil, rc, err
	}
	if rc.HostKey == "" {
		rc.HostKey = seen
		pin := rc
		s.Store.UpdateGateway(g.ID, true, func(x *model.Gateway) { x.Remote = &pin })
	}
	return &remoteSession{c}, rc, nil
}

func (r *remoteSession) run(cmd string, stdin []byte) (string, error) {
	sess, err := r.client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	if stdin != nil {
		sess.Stdin = bytes.NewReader(stdin)
	}
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		b, e := sess.CombinedOutput(cmd)
		done <- result{b, e}
	}()
	select {
	case res := <-done:
		return string(res.out), res.err
	case <-time.After(remoteTimeout):
		return "", errors.New("the gateway did not answer in time")
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (s *Server) remoteGateway(w http.ResponseWriter, r *http.Request) (*model.Gateway, bool) {
	g, err := s.Store.Gateway(r.PathValue("id"))
	if err != nil {
		storeErr(w, err)
		return nil, false
	}
	return g, true
}

func (s *Server) getRemote(w http.ResponseWriter, r *http.Request) {
	g, ok := s.remoteGateway(w, r)
	if !ok {
		return
	}
	sess, rc, err := s.dialRemote(g)
	out := map[string]any{"remote": rc}
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}
	defer sess.client.Close()
	if b, err := sess.run("cat "+shellQuote(rc.ConfigPath), nil); err != nil {
		out["config_error"] = strings.TrimSpace(b)
	} else {
		out["config"] = json.RawMessage(b)
		if !json.Valid([]byte(b)) {
			delete(out, "config")
			out["config_error"] = "the config file on the gateway is not valid JSON"
		}
	}
	if b, _ := sess.run("nc 127.0.0.1 5557 2>/dev/null </dev/null", nil); json.Valid([]byte(b)) && b != "" {
		out["status"] = json.RawMessage(b)
	}
	b, _ := sess.run("pidof raw_lora_app sx1276_app lg02_pkt_fwd lg01_pkt_fwd", nil)
	out["running"] = strings.TrimSpace(b) != ""
	writeJSON(w, 200, out)
}

func (s *Server) putRemote(w http.ResponseWriter, r *http.Request) {
	g, ok := s.remoteGateway(w, r)
	if !ok {
		return
	}
	var req struct {
		Host        string `json:"host"`
		Port        int    `json:"port"`
		User        string `json:"user"`
		ConfigPath  string `json:"config_path"`
		TrustNewKey bool   `json:"trust_new_key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.Port < 1 || req.Port > 65535 || req.User == "" || strings.ContainsAny(req.User, " '\"") || !remotePath.MatchString(req.ConfigPath) || strings.ContainsAny(req.Host, " '\"/") {
		writeErr(w, 400, "check the address, port, user and config path")
		return
	}
	rc := remoteOf(g)
	if req.Host != rc.Host || req.TrustNewKey {
		rc.HostKey = ""
	}
	rc.Host, rc.Port, rc.User, rc.ConfigPath = req.Host, req.Port, req.User, req.ConfigPath
	if err := s.Store.UpdateGateway(g.ID, true, func(x *model.Gateway) { x.Remote = &rc }); err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, 200, rc)
}

func (s *Server) putRemoteConfig(w http.ResponseWriter, r *http.Request) {
	g, ok := s.remoteGateway(w, r)
	if !ok {
		return
	}
	var cfg map[string]any
	if err := readJSON(r, &cfg); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	body, _ := json.MarshalIndent(cfg, "", "    ")
	sess, rc, err := s.dialRemote(g)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	defer sess.client.Close()
	p := shellQuote(rc.ConfigPath)
	tmp := shellQuote(rc.ConfigPath + ".new")
	bak := shellQuote(rc.ConfigPath + ".bak")
	if out, err := sess.run(fmt.Sprintf("cat > %s && cp %s %s 2>/dev/null; mv %s %s", tmp, p, bak, tmp, p), body); err != nil {
		writeErr(w, 502, strings.TrimSpace(out+" "+err.Error()))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) remoteService(w http.ResponseWriter, r *http.Request) {
	g, ok := s.remoteGateway(w, r)
	if !ok {
		return
	}
	var req struct {
		Cmd string `json:"cmd"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !serviceCmds[req.Cmd] {
		writeErr(w, 400, "cmd must be start, stop or restart")
		return
	}
	sess, _, err := s.dialRemote(g)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	defer sess.client.Close()
	if out, err := sess.run(fmt.Sprintf("if [ -x /etc/init.d/lora_gateway ]; then /etc/init.d/lora_gateway %s; else systemctl %s raw-lora; fi", req.Cmd, req.Cmd), nil); err != nil {
		writeErr(w, 502, strings.TrimSpace(out+" "+err.Error()))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) remoteLogs(w http.ResponseWriter, r *http.Request) {
	g, ok := s.remoteGateway(w, r)
	if !ok {
		return
	}
	sess, _, err := s.dialRemote(g)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	defer sess.client.Close()
	out, _ := sess.run("if [ -f /var/log/lora.log ]; then tail -n 200 /var/log/lora.log; else journalctl -u raw-lora -n 200 --no-pager; fi", nil)
	writeJSON(w, 200, map[string]string{"log": out})
}
