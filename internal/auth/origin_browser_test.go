package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestLocalOriginBrowser is opt-in: it uses an already installed Playwright
// module and Chromium, without adding a production dependency or downloading
// a browser. The page and credentials are synthetic; the browser is real,
// but its authenticator is virtual. This does not cover Relay's complete UI.
func TestLocalOriginBrowser(t *testing.T) {
	module := os.Getenv("RELAY_ORIGIN_BROWSER_MODULE")
	chrome := os.Getenv("RELAY_ORIGIN_BROWSER_CHROME")
	if module == "" || chrome == "" {
		t.Skip("set RELAY_ORIGIN_BROWSER_MODULE and RELAY_ORIGIN_BROWSER_CHROME for Chromium virtual-authenticator coverage")
	}
	e := newEnv(t, "localhost")
	e.setup()
	page := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><title>Relay synthetic origin fixture</title>"))
	}
	e.rt.Public("GET /fixture", page)
	_, port, _ := net.SplitHostPort(e.d.Cfg.Server.Listen)
	ln, err := net.Listen("tcp", "[::1]:"+port)
	if err != nil {
		t.Fatal(err)
	}
	v6 := &httptest.Server{Listener: ln, Config: &http.Server{Handler: e.srv.Config.Handler}}
	v6.Start()
	t.Cleanup(v6.Close)
	// Choose an available different port inside Relay's shared-machine range.
	// Auth fixtures use 47740–47779; an OS-assigned port could escape the range.
	var wrongPort net.Listener
	for candidate := 47700; candidate <= 47738; candidate++ {
		wrongPort, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", candidate))
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("no free browser fixture port in 47700–47738: %v", err)
	}
	_, rejectedPort, _ := net.SplitHostPort(wrongPort.Addr().String())
	rejectedOrigin := "http://localhost:" + rejectedPort
	other := &httptest.Server{Listener: wrongPort, Config: &http.Server{Handler: http.HandlerFunc(page)}}
	other.Start()
	t.Cleanup(other.Close)
	ip := newEnv(t, "127.0.0.1")
	ip.setup()
	ip.rt.Public("GET /fixture", page)
	_, ipPort, _ := net.SplitHostPort(ip.d.Cfg.Server.Listen)
	script := filepath.Join(t.TempDir(), "origins.mjs")
	if err := os.WriteFile(script, []byte(originBrowserScript), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", script, module, chrome, port, ipPort, rejectedPort, testPassword)
	// Synthetic password is consumed by the child; never log argv or cookies.
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser fixture: %v\n%s", err, output)
	}
	select {
	case origin := <-e.rejections:
		if origin != rejectedOrigin {
			t.Fatalf("rejected browser Origin = %q", origin)
		}
		t.Logf("actual browser different-port POST from %s reached the server and received 403", rejectedOrigin)
	default:
		t.Fatal("different-port browser POST did not reach the Origin guard")
	}
	t.Logf("Chromium fixture result: %s", output)
}

const originBrowserScript = `
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';
const [module, chrome, port, ipPort, rejectedPort, password] = process.argv.slice(2);
const playwright = await import(pathToFileURL(module).href);
const { chromium } = playwright.default || playwright;
const browser = await chromium.launch({executablePath: chrome, headless: true, args: ['--no-sandbox']});
const results = {browser: browser.version(), authenticator: 'Chromium CDP virtual CTAP2; synthetic account', aliases: []};
try {
  for (const host of ['localhost', '127.0.0.1', '[::1]']) {
    const context = await browser.newContext();
    const page = await context.newPage();
    await page.goto('http://' + host + ':' + port + '/fixture');
    const result = await page.evaluate(async ({password, host}) => {
      const request = async (path, body, method = 'POST') => {
        const r = await fetch('/api/v1/' + path, {method, headers: {'Content-Type': 'application/json'}, body: method === 'GET' ? undefined : JSON.stringify(body)});
        return {status: r.status, body: await r.json()};
      };
      const login = await request('auth/login', {username: 'owner', password, remember: false});
      const unsafe = await request('test/whoami', {});
      const state = await request('auth/state', undefined, 'GET');
      const begin = await request('auth/passkey/begin', {});
      return {host, secureContext: isSecureContext, login: login.status, unsafe: unsafe.status,
        available: state.body.passkeysAvailable, setupPasskey: !!login.body.setupPasskey,
        begin: begin.status, authenticated: state.body.authenticated};
    }, {password, host});
    assert.equal(result.secureContext, true);
    assert.equal(result.login, 200);
    assert.equal(result.unsafe, 200);
    assert.equal(result.authenticated, true);
    assert.equal(result.available, host === 'localhost');
    assert.equal(result.setupPasskey, host === 'localhost');
    assert.equal(result.begin, host === 'localhost' ? 200 : 503);
    results.aliases.push(result);
    await context.close();
  }
  const context = await browser.newContext();
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', {options: {
    protocol: 'ctap2', transport: 'internal', hasResidentKey: true,
    hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true
  }});
  await page.goto('http://localhost:' + port + '/fixture');
  const passkey = await page.evaluate(async (password) => {
    const post = async (path, body = {}) => {
      const r = await fetch('/api/v1/auth/' + path, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)});
      return {status: r.status, body: r.status === 204 ? null : await r.json()};
    };
    await post('login', {username: 'owner', password, remember: false});
    const options = await post('passkeys/begin', {name: 'Synthetic Chromium authenticator'});
    const credential = await navigator.credentials.create({publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(options.body.publicKey)});
    const registration = await post('passkeys/finish', credential.toJSON());
    await post('logout');
    const assertionOptions = await post('passkey/begin');
    const assertion = await navigator.credentials.get({publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(assertionOptions.body.publicKey)});
    const signin = await post('passkey/finish', assertion.toJSON());
    return {rpID: options.body.publicKey.rp.id, registration: registration.status,
      signin: signin.status, discoverable: (assertionOptions.body.publicKey.allowCredentials || []).length === 0,
      authenticated: (await (await fetch('/api/v1/auth/state')).json()).authenticated};
  }, password);
  assert.deepEqual(passkey, {rpID: 'localhost', registration: 201, signin: 200, discoverable: true, authenticated: true});
  results.passkey = passkey;
  // Keep the same hostname's cookie while navigating to an unexpected port.
  await page.goto('http://localhost:' + rejectedPort + '/fixture');
  const rejected = await page.evaluate(async (port) => {
    try { await fetch('http://localhost:' + port + '/api/v1/test/whoami', {method: 'POST', credentials: 'include', body: ''}); return false; } catch { return true; }
  }, port);
  assert.equal(rejected, true);
  // CORS hides the response from JS; the Go fixture independently asserts
  // that this actual browser request reached its Origin guard and got 403.
  results.differentPort = 'blocked by browser; server 403 asserted by Go';
  await context.close();
  const ipContext = await browser.newContext();
  const ipPage = await ipContext.newPage();
  await ipPage.goto('http://localhost:' + ipPort + '/fixture');
  const ipState = await ipPage.evaluate(async () => (await (await fetch('/api/v1/auth/state')).json()).passkeysAvailable);
  assert.equal(ipState, false);
  results.ipCanonicalAtLocalhost = false;
  await ipContext.close();
  console.log(JSON.stringify(results));
} finally {
  await browser.close();
}
`
