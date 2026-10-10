# Sign in

On a new instance the login page offers to create the owner account. After that account exists, the server reports that setup is no longer required.

## Sub-features

- `sign-in-first-run`. `/login` shows the heading "Create your account".
- `sign-in-setup`. Creating the account makes `GET /api/v1/auth/state` report `setupRequired` false.

## How to get to it (user POV)

- Open `/login`. On first run the heading `h1#login-title` is "Create your account", the fields are labelled Username and Password, and the submit button is "Create account".
- After an account exists the same page's heading is "Sign in" and the submit button is "Sign in".

## Driving it with control-relay

Preconditions:

- `ctl doctor` passes for a run started by `ctl up`.
- For the browser step, `pnpm --dir scripts/dev install` has been run, and `CHROME` is a Chromium binary. On this machine that is `/Applications/Brave Browser.app/Contents/MacOS/Brave Browser`.

- **Open the login page.** The user goes to `/login`. Run `ctl browser text /login --out /tmp/rv-login.txt`. The snapshot contains `Create your account`.
- **Create the account.** The user submits Username `verifier` and a synthetic password. The password is at least 10 characters, is not the username, is not a common password, and uses at least 4 different characters. `POST /api/v1/auth/setup` from this machine does not need a setup code. Run it with `ctl api POST /api/v1/auth/setup --data <json> --out /tmp/rv-setup.json`. Do not write the password into the bundle. The response contains `"ok":true`.
- **Read it back.** The user would see Sign in on the next visit. Run `ctl api GET /api/v1/auth/state --out /tmp/rv-auth-state.json`. The body contains `"setupRequired":false`.
- **Proof.** Record each step with `ctl evidence add`. Mark the auth-state step with `--readback`.

## Gotchas

- Input ids on the login form are generated and change between loads. Use `h1#login-title` and the visible labels.
- `ctl api` keeps no cookie. After setup, `authenticated` stays false on `GET /api/v1/auth/state` until a later request sends the session cookie. This recipe checks `setupRequired`, not a signed-in page.
- `relay setup` without `--listen` uses port 7777, which is outside 47700-47799. Pass `--listen 127.0.0.1:<port>` and `--no-systemd`.
- The first-run serve log prints a one-time setup code. Do not copy that log into a bundle, a commit or a reply.
- `VITE_MOCK=1` is not this page.
