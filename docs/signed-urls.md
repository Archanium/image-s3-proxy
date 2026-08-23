# Signed image URLs

Client reference for the `/_p/` processing-option route: the option vocabulary,
the signing algorithm, and test vectors to check your implementation against.

For operator-facing configuration (environment variables, storage modes, deploy),
see [`../README.md`](../README.md).

```
/_p/{signature}/{option}/{option}/.../{source-tail}
```

---

## Contents

- [Anatomy of a URL](#anatomy-of-a-url)
- [Signing](#signing)
- [Test vectors](#test-vectors)
- [Source tails](#source-tails)
- [Pipeline order](#pipeline-order)
- [Options](#options)
- [Resizing types](#resizing-types)
- [Gravity](#gravity)
- [Passthrough](#passthrough)
- [Transparency](#transparency)
- [Errors](#errors)
- [Trying it locally](#trying-it-locally)
- [Checklist](#checklist)

---

## Anatomy of a URL

One slice of the URL is authenticated; a different slice decides which cached
object you get. Knowing where the boundary sits explains most of the behaviour
below.

```
/_p/  {signature}  /rt:fill/w:240/h:336   /13/products/foo.jpg
      ~~~~~~~~~~~   ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
      not signed    |<--- signed: HMAC over salt || this exact string --->|

                    |<--- canonicalised --->|<-- carried verbatim -->|
                                v                       v
      cache key:  13/_p/height=336,resizing_type=fill,width=240/products/foo.jpg
```

Two consequences worth internalising:

- **The signature covers the URL as written.** Reorder the options and you must
  re-sign — the signature changes.
- **The cache key covers what the URL means.** Reordering, switching to aliases,
  or stating a default explicitly all produce the *same* cached object. Three
  differently spelled URLs share one object on disk.

You therefore do not need to normalise option order for the proxy's cache — it
does that itself. Pick a consistent order anyway, so identical requests produce
identical URLs and any CDN in front of the proxy also gets a hit.

---

## Signing

This is imgproxy's scheme, unchanged. If you already have an imgproxy URL
builder, it works against this service as-is.

1. Build the path: `"/" + options.join("/") + "/" + sourceTail` — everything
   after `/_p/{signature}`, **including the leading slash**.
2. Hex-decode `SIGNATURE_KEY` and `SIGNATURE_SALT`.
3. Compute `HMAC-SHA256(key, salt || path)` — the salt is prepended to the
   *message*, it is not the key.
4. Truncate the digest to `SIGNATURE_SIZE` bytes (default 32, the full digest).
5. Encode as **URL-safe base64 without padding**.
6. Prepend `/_p/{signature}` to the path.

> **The most common mistake** is signing the whole request path, `/_p/` included.
> The `/_p/` prefix and the signature segment are *not* part of the signed
> string. Sign only what follows them.

### PHP

```php
function imageProxyUrl(
    string $keyHex,
    string $saltHex,
    array $options,
    string $source,
    int $size = 32
): string {
    $path   = '/' . implode('/', array_merge($options, [$source]));
    $digest = hash_hmac('sha256', hex2bin($saltHex) . $path, hex2bin($keyHex), true);
    $sig    = rtrim(strtr(base64_encode(substr($digest, 0, $size)), '+/', '-_'), '=');

    return '/_p/' . $sig . $path;
}

echo imageProxyUrl(
    $_ENV['SIGNATURE_KEY'],
    $_ENV['SIGNATURE_SALT'],
    ['rs:fill:240:336:1', 'bg:fff'],
    '13/products/foo.jpg'
);
```

### JavaScript

```js
import { createHmac } from 'node:crypto';

function imageProxyUrl(keyHex, saltHex, options, source, size = 32) {
  const path = '/' + [...options, source].join('/');
  const digest = createHmac('sha256', Buffer.from(keyHex, 'hex'))
    .update(Buffer.from(saltHex, 'hex'))
    .update(path)
    .digest()
    .subarray(0, size);

  return '/_p/' + digest.toString('base64url') + path;
}
```

### Python

```python
import base64
import binascii
import hashlib
import hmac


def image_proxy_url(key_hex, salt_hex, options, source, size=32):
    path = "/" + "/".join([*options, source])
    digest = hmac.new(
        binascii.unhexlify(key_hex),
        binascii.unhexlify(salt_hex) + path.encode(),
        hashlib.sha256,
    ).digest()[:size]

    return "/_p/" + base64.urlsafe_b64encode(digest).rstrip(b"=").decode() + path
```

### Go

```go
func ImageProxyURL(keyHex, saltHex string, options []string, source string, size int) (string, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return "", err
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return "", err
	}

	path := "/" + strings.Join(append(append([]string{}, options...), source), "/")

	mac := hmac.New(sha256.New, key)
	mac.Write(salt)
	mac.Write([]byte(path))

	return "/_p/" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:size]) + path, nil
}
```

All four produce the same URL from the same inputs; each was executed against the
vectors below.

---

## Test vectors

Check your signer against these before wiring anything up. Throwaway key and salt,
so you can run them locally.

```
SIGNATURE_KEY  = 6d792d7365637265742d6b6579   # hex of "my-secret-key"
SIGNATURE_SALT = 6d792d73616c74               # hex of "my-salt"
```

| Path to sign | Size | Expected signature |
|---|---:|---|
| `/13/products/foo.jpg` | 32 | `yLDzk3_zW4scHp9uCk_f2bBrT2A0gIi5j1zbREn1IJY` |
| `/w:800/13/products/foo.jpg` | 32 | `7Hm7y5cbp8X7ITmYNKJ8I_xShov_UVWM5DGYuJqWkV0` |
| `/rt:fill/w:240/h:336/13/products/foo.jpg` | 32 | `MRL_1zEFW2a2gczXmGMdkTb58Fx0dFDCuMcbuAnbtcc` |
| `/rs:fill:240:336:1/bg:fff/13/products/foo.jpg` | 32 | `EfJN7y4nlsfYrvbbyeE0E8fgeWmHpAgCSldArUNfrJA` |
| `/c:0.5:0.5:sm/w:600/13/branding/logo.png.webp` | 32 | `4t0CCZ3GT2FrH_2g--a3GWzRyIdodS2MZa5sUP9QPnY` |
| `/raw:1/13/files/42/doc.pdf` | 32 | `e0IRZEIC4bccjcB6xzc3KT4kv5XcKzkgMk0qvlCAw28` |
| `/w:800/13/products/foo.jpg` | 8 | `7Hm7y5cbp8U` |

The last row is the same path as the second, truncated to 8 bytes — useful for
confirming your code truncates the *digest* before base64, not the base64 string
after.

---

## Source tails

The source tail names the original. Two shapes are recognised; anything else is a
`404` rather than a guess.

| Tail | Resolves to |
|---|---|
| `{clientId}[-{group}]/{folder}/{file}` | A catalog image: `{clientId}/catalog/{folder}/images/{file}` first, then several historical layouts, then common extension substitutions |
| `{clientId}/files/{fileId}/{file}` | That literal key, no candidate ladder |

These are the same layouts the legacy URL families resolve, including the
legacy-bucket fallback with its copy-on-read migration. Segment counts are exact:
three for a catalog image, four for a file. Deeper paths are not supported.

### Output format

The output format is the **last dot-suffix of the filename**. A compound extension
names the source with everything before the last suffix:

| Filename in the URL | Source looked up | Encoded as |
|---|---|---|
| `foo.jpg` | `foo.jpg` | JPEG |
| `foo.webp` | `foo.webp` | WebP |
| `logo.png.webp` | `logo.png` | WebP |
| `hero.jpg.avif` | `hero.jpg` | AVIF |

Encodable formats are `png`, `jpg`, `jpeg`, `webp`, `avif`. Anything else is a
[passthrough](#passthrough). A filename with no extension is a `404`.

---

## Pipeline order

Options are not applied in the order you write them. The pipeline is fixed:

```
  crop  ->  resize  ->  extend  ->  background  ->  encode
   c:      rt: w: h: el:   ex: exar:      bg:       from the extension
```

Crop selects a region of the *source*, before any scaling. Extend pads the result
*after* scaling.

The consequence that surprises people: `c:200:200/w:100` crops a 200x200 region
out of the original and then fits it to width 100, giving a square. If resizing
ran first it would give something else entirely.

---

## Options

Thirteen options, each written `name:arg:arg`. Every option segment contains a
colon — that is how the proxy tells options from the source tail.

Booleans accept `1`, `t`, `true` and `0`, `f`, `false`. Omitting an argument
leaves that field at its default, so `rs::240` sets only the width.

### Geometry

| Option | Aliases | Arguments | Default | Notes |
|---|---|---|---|---|
| `width` | `w` | `width:%width` | `0` | `0` derives it from the height and the source ratio |
| `height` | `h` | `height:%height` | `0` | `0` derives it from the width and the source ratio |
| `resizing_type` | `rt` | `resizing_type:%type` | `fit` | See [Resizing types](#resizing-types) |
| `enlarge` | `el` | `enlarge:%enlarge` | `0` | Permit upscaling a source smaller than the box |
| `resize` | `rs` | `resize:%type:%width:%height:%enlarge:%extend` | — | Shorthand for the four above plus extend |
| `size` | `s` | `size:%width:%height:%enlarge:%extend` | — | Shorthand without the resizing type |

### Cropping

| Option | Aliases | Arguments | Default | Notes |
|---|---|---|---|---|
| `crop` | `c` | `crop:%width:%height:%gravity` | — | Applied **before** resizing. `0` means the full dimension; below `1` is a fraction of it; anything else is pixels |
| `gravity` | `g` | `gravity:%type:%x:%y` | `ce` | The anchor `fill` crops around. See [Gravity](#gravity) |

### Padding and colour

| Option | Aliases | Arguments | Default | Notes |
|---|---|---|---|---|
| `extend` | `ex` | `extend:%extend:%gravity` | `0` | Pads out to the requested *size*. Needs both width and height set |
| `extend_aspect_ratio` | `extend_ar`, `exar` | `extend_aspect_ratio:%extend:%gravity` | `0` | Pads out to the requested *ratio* — a 100x50 result asked for 400x400 becomes 100x100, not 400x400 |
| `background` | `bg` | `background:%hex` or `background:%R:%G:%B` | — | Fills padding and flattens transparency. `fff`, `ffffff` and `255:255:255` are the same colour |

### Bypass

| Option | Aliases | Arguments | Default | Notes |
|---|---|---|---|---|
| `raw` | *(none)* | `raw:%raw` | `0` | Serve the original bytes untouched |
| `skip_processing` | `skp` | `skip_processing:%ext:%ext:...` | — | Serve untouched when the output extension matches |

> Writing an option at its default costs nothing and changes no cache key —
> `w:240` and `w:240/rt:fit/el:0/h:0` are the same request. Write whichever is
> clearer in your builder.

---

## Resizing types

| Value | Behaviour |
|---|---|
| `fit` *(default)* | Scale to sit inside the box, ratio preserved, nothing cropped |
| `fill` | Scale to cover the box, ratio preserved, crop the excess at the gravity |
| `fill-down` | As `fill`, but never enlarges. A 100x50 source asked for 400x400 gives 50x50 — the largest square the pixels allow |
| `force` | Stretch to exactly the box, ratio ignored. This one ignores `enlarge` |
| `auto` | `fill` when the source and the box share an orientation, `fit` otherwise |

A 2000x900 source asked for various boxes, verified end to end against the real
service:

| URL options | Result |
|---|---|
| `rt:fit/w:400/h:400` | 400x180 |
| `rt:fill/w:240/h:240` | 240x240 |
| `rt:force/w:300/h:300` | 300x300 |
| `w:500` | 500x225 |
| `h:450` | 1000x450 |
| `c:0.5:0.5/w:200` | 200x90 |
| `rt:fit/w:400/h:400/ex:1` | 400x400 |
| `rt:fit/w:400/h:400/exar:1` | 400x400 |

> **`fill` vs `fill-down`:** `rt:fill` with `el:0` — the default — behaves
> identically to `rt:fill-down`. The two only diverge once you add `el:1`, which
> lets `fill` upscale and leaves `fill-down` unchanged.

---

## Gravity

Which part of the image survives a crop, and where a smaller image sits when
padded. Compass abbreviations, north-up:

```
  +--------+--------+--------+
  |  nowe  |   no   |  noea  |
  +--------+--------+--------+
  |   we   |   ce   |   ea   |
  |        |(default)|       |
  +--------+--------+--------+
  |  sowe  |   so   |  soea  |
  +--------+--------+--------+
```

Two non-positional values:

- `sm` — smart. libvips picks the most visually interesting region. Crops only;
  it is rejected on `extend` and `extend_aspect_ratio`, where there is nothing to
  be smart about. Takes no offsets.
- `fp:%x:%y` — focus point. `x` and `y` are fractions of the source, so
  `fp:0.5:0.25` centres the crop a quarter of the way down the middle. Good for
  product shots with a known subject position.

The positional values take optional offsets — `g:so:0:20` — which move the box
inward from the edge the gravity names.

---

## Passthrough

Three conditions make the proxy serve the original bytes and skip processing:

- `raw:1`
- `skip_processing` names the output extension
- the extension is not encodable — anything outside `png`, `jpg`, `jpeg`,
  `webp`, `avif`

That third condition is what makes `/_p/{sig}/13/files/42/doc.pdf` return a
working PDF rather than a PDF re-encoded as a JPEG.

> **Passthrough is not cached.** The object already exists in the origin under its
> own key, so a second copy buys nothing. Every passthrough request reads the
> origin. If you serve the same untouched asset at high volume, point your CDN at
> it rather than relying on the proxy's cache.

---

## Transparency

`/_p/` follows imgproxy's rule: **transparency survives into any alpha-capable
output format** (`png`, `webp`, `avif`), and `background` is what flattens it.
`jpg`/`jpeg` have no alpha channel, so they always flatten — to the requested
background, or to white if none was given.

This **differs from the legacy routes**, which default to flattening a
transparent *raster* original to white and keep alpha only for SVG sources. The
same source and the same output format therefore give different results
depending on which vocabulary you use:

| Request | Transparent PNG source |
|---|---|
| `/13/1/images/products/240/336/foo.png` | flattened to white |
| `/_p/{sig}/w:240/h:336/13/products/foo.png` | alpha preserved |

That is deliberate — `/_p/` is a new contract and matches imgproxy rather than
the historical Node.js behaviour. If you are porting a legacy URL and want the
old look, add an explicit background:

```
/_p/{sig}/w:240/h:336/bg:fff/13/products/foo.png
```

The legacy `flat/` and `alpha/` URL segments have no `/_p/` equivalent, and do
not need one: `bg:` covers flattening, and omitting it covers keeping.

---

## Errors

| Code | Means | What to check |
|---|---|---|
| `200` | Served, from cache or freshly processed | — |
| `400` | An option is unknown, malformed, or over `MAX_DIMENSION` | The body names the offending option. Dimensions default to a 5120 ceiling |
| `403` | Signature missing, wrong, or `unsafe` against a production proxy | You almost certainly signed the wrong string — see [Signing](#signing) |
| `404` | Route switched off, source tail unrecognised, or original missing | Confirm the proxy has signing keys configured, then check the segment count and the file extension |
| `500` | The resize itself failed | Usually a corrupt or unsupported original. Check the proxy logs by `X-Request-ID` |

Errors carry `Cache-Control: max-age=30` so a CDN retries quickly; successes carry
`max-age=31536000`. Every response echoes an `X-Request-ID` you can use to find
the matching access-log line.

> **A `404` on every `/_p/` URL** is what a proxy with no `SIGNATURE_KEY` and
> `SIGNATURE_SALT` returns. The route is fail-closed and does not exist until it
> is configured — it is not a problem with your URL. Ask whoever runs the
> environment whether signing is switched on there.

---

## Trying it locally

`docker-compose.yml` sets `ALLOW_UNSAFE_URLS=true` for the `app` and `app-debian`
services, so a local proxy accepts the literal signature `unsafe` and you can
hand-write URLs while exploring:

```bash
make up
curl -sI 'http://localhost:8080/_p/unsafe/rt:fill/w:240/h:336/13/products/foo.jpg'
```

To exercise real signing locally instead:

```bash
SIGNATURE_KEY=6d792d7365637265742d6b6579 \
SIGNATURE_SALT=6d792d73616c74 \
ALLOW_UNSAFE_URLS=false \
docker compose up -d app
```

`ALLOW_UNSAFE_URLS` is local-only and must never be set in the k3s deployment. A
production proxy answers `403` for `unsafe`, exactly as it does for a wrong
signature.

### Pointing at MinIO

The AWS SDK addresses buckets **virtual-host style** (`bucket.endpoint`), which
works against Hetzner Object Storage and R2 but needs two extra pieces for MinIO:
start MinIO with `MINIO_DOMAIN` set, and give its container a network alias
matching `{bucket}.{host}`. Without both you get `InvalidBucketName` or a DNS
failure, not a proxy bug.

---

## Checklist

Before shipping a URL builder:

- [ ] Your signer reproduces all seven [test vectors](#test-vectors), including
      the 8-byte truncated one.
- [ ] You sign only what follows `/_p/{signature}`, with a leading slash.
- [ ] The key and salt are read as **hex** and decoded before use.
- [ ] Base64 is URL-safe and unpadded.
- [ ] Your builder emits options in a stable order, so identical requests give
      identical URLs and your CDN caches them.
- [ ] Requested dimensions stay under `MAX_DIMENSION` — 5120 unless your
      environment says otherwise.
- [ ] Source keys contain no characters needing percent-encoding. The catalog
      alphabet (letters, digits, dot, dash, underscore) is safe; spaces and
      non-ASCII are not supported.
- [ ] You have a fallback for `403` and `404` — a placeholder image beats a broken
      one.

> **The legacy URLs still work.** Nothing about
> `/13/2/images/products/240/336/foo.jpg` has changed, and those URLs are not
> signed. You can adopt `/_p/` incrementally, one surface at a time, with both
> vocabularies live against the same buckets.
