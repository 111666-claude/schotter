<p align="center"><img width="800" src="images/header.jpg"></a></p>

<p align="center">
  <a href="https://goreportcard.com/report/github.com/tja/schotter"><img src="https://goreportcard.com/badge/github.com/tja/schotter" alt="Go Report Card"></a>
  <a href="https://github.com/tja/schotter/blob/master/LICENSE"><img src="http://img.shields.io/badge/license-MIT-brightgreen.svg" alt="MIT License"></a>
</p>

# Schotter

**Schotter** will generate line art inspired by [Georg Nees](https://en.wikipedia.org/wiki/Georg_Nees)' graphic
[*Gravel*](https://collections.vam.ac.uk/item/O221321/schotter-print-nees-georg/) (*Schotter* in German). The
resulting SVG images can be used to drive a plotter &mdash; such as [Evil Mad Scientist's
AxiDraw](https://axidraw.com) &mdash; to create beautiful pieces of art.

<p align="center"><img width="800" src="images/axidraw.jpg"></a></p>

## Installation

Pre-built binaries are available on the [release page](https://github.com/tja/schotter/releases/latest). Simply
download, make executable, and move it to a folder in your `PATH`:

```bash
curl -L https://github.com/tja/schotter/releases/download/v1.1.0/schotter-`uname -s`-`uname -m` >/tmp/schotter
chmod +x /tmp/schotter
sudo mv /tmp/schotter /usr/local/bin/schotter
```

## Usage

To generate a new image named `sample.svg` Run Schotter like this:

```bash
schotter sample.svg
```

Run `schotter --help` to see the list of available options.

## Animation

Instead of randomizing every frame independently (which flickers), Schotter
generates one target layout for the given `--seed` and lets each square travel
on a straight line from its position on the ordered grid to its final
position. Adjacent frames therefore differ by a constant, small displacement
and the animation evolves smoothly from ordered to scattered.

Export an animated GIF (held in memory, capped at 500 frames):

```bash
schotter --seed 42 --frames 24 --delay 8 animation.gif
```

Export an ordered PNG sequence (streamed one frame at a time, so memory stays
bounded by a single frame regardless of length):

```bash
schotter --seed 42 --frames 24 'frame_%04d.png'
```

The same `--seed` always reproduces the same frames, and the last animation
frame is pixel-identical to a single-frame render with the same parameters.
`--disturbance 0` renders the perfectly ordered grid; values above 1 amplify
the scatter. Existing outputs are never overwritten unless `--force` is
passed, and an interrupted export (Ctrl-C) removes its partial file and exits
with code 130.

| Situation | Result |
| --- | --- |
| `--frames 0` (default) | Original single SVG mode, unchanged |
| `--frames 1 out.png` | Single PNG of the final state |
| Negative `--disturbance` / `--frames` | Usage error, exit code 2 |
| GIF above 500 frames or 256 MiB of pixels | Runtime error, exit code 1, no file written |
| PNG sequence above 100 000 frames, or canvas above 64 MP | Rejected before allocation |
| Output path already exists | Error unless `--force` is given |
| Ctrl-C during export | Partial file removed, exit code 130 |

## License

Copyright (c) 2018 Thomas Jansen. Released under the
[MIT License](https://github.com/tja/schotter/blob/master/LICENSE).
