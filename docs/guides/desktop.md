---
title: Desktop
description: A remote Linux desktop in your browser with Chrome and Blender, clipboard sync and touch controls. Your agents can drive the same screen.
---

Sometimes a terminal is not enough. You may need a real browser to sign in
to a service, check a layout, or use a graphical app such as Blender.
**Desktop** starts a lightweight Linux desktop on your machine and shows it
in your browser. It starts when you open it and stops when nobody has
looked at it for a while. Agents running in Relay terminals can use the same
screen, so you can watch an agent browse.

:::note
The desktop needs Linux with an X server (TigerVNC). It is not available on
macOS.
:::

## Install the desktop packages

The desktop needs a few small packages: TigerVNC, the Openbox window manager,
the tint2 panel and some clipboard and window tools. Install them once:

1. Open **Toolbox** and find **Desktop** in the *desktop* category.
2. Select **Install**. The install runs in a terminal. It uses `apt` and asks
   for your `sudo` password.

The equivalent command on Ubuntu or Debian:

```bash
sudo apt-get install -y --no-install-recommends \
  tigervnc-standalone-server openbox tint2 xclip xdotool x11-xserver-utils
```

Also install **Google Chrome** (or Chromium) and **Blender** from the
Toolbox if you want them.

## Start the desktop

1. Open **Desktop** in the navigation.
2. Select **Start desktop**. After a second or two, you see a dark desktop
   with a panel at the bottom.
3. Launch apps from the **launcher** in Relay (Chrome, Blender, Files,
   Terminal and any apps you configured) or from the panel.

Select **Stop desktop** when you are done. If you forget, Relay stops it
after 2 hours with no viewer
([`desktop.idle_stop`](../reference/configuration.md#desktop)). Your apps
close when the desktop stops.

## Use the viewer

| Control | What it does |
| --- | --- |
| **Fit / 1:1** | Scale the desktop to your window, or show real pixels |
| **Quality** | Trade sharpness for speed. **Auto** adjusts to your connection |
| **Full screen** | Hide everything except the desktop |
| **Resolution** | Change the desktop size, for example to match your screen |
| **Clipboard** | Sync text both ways (see below) |

Window snapping works with the keyboard: **Super+←** and **Super+→** snap a
window to the left or right half of the screen, and **Super+↑** maximises
it.

## Use it on a phone or tablet

The viewer has touch controls:

- **Touchpad mode** (default): drag anywhere to move the pointer, and tap
  to click, like a laptop touchpad. This is precise on a small screen.
- **Two-finger tap** or the **right-click** button: right-click.
- **Two-finger drag:** scroll.
- **Keyboard** button: shows your phone keyboard, to type into the desktop.
- **Pinch:** zoom the view.

## Copy and paste

Clipboard sync works in both directions:

- Text you copy **on the desktop** appears in the viewer's clipboard panel
  and in Relay's [clipboard history](command-center.md#use-clipboard-history),
  so you can paste it on any device.
- To send text **to the desktop**, paste it into the clipboard panel and
  select **Send**. Then paste on the desktop with Ctrl+V.

## Let agents use the desktop

While the desktop runs, new Relay terminals get the `DISPLAY` variable
pointing at it. Any graphical program an agent starts, including Chrome
driven by Playwright or DevTools, appears on your desktop, where you can
watch it and take over.

To let your agents control a browser there:

1. Open **Toolbox → MCP servers**.
2. Apply **Playwright** or **Chrome DevTools** to the agents you use. See
   [Toolbox → Connect MCP servers](toolbox.md#connect-mcp-servers-to-your-agents).
3. Start the desktop, then start the agent session in a new terminal (so
   it gets `DISPLAY`).
4. Ask the agent to open a page. You see Chrome open on the desktop and
   follow the agent's clicks.

Chrome launched from Relay uses its own profile, stored in Relay's data
folder. It does not share cookies or sign-ins with any other Chrome on the
machine.

## Add your own apps to the launcher

List extra apps in `relay.toml` under [`[[desktop.apps]]`](../reference/configuration.md#desktop):

```toml
[[desktop.apps]]
id = "gimp"
name = "GIMP"
command = ["gimp"]
```

Restart Relay to see them in the launcher.

## Security

The desktop has no network port. It listens only on a private socket that
belongs to your user, and Relay connects your browser to it after you sign
in. Nobody else on the machine or on the internet can connect to it
directly.

## Next steps

- [Toolbox](toolbox.md): Chrome, Blender and MCP servers.
- [Previews](previews.md): often enough, and lighter than a full desktop.
- [Code](code.md)
