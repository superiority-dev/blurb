# blurb

[![CI](https://github.com/superiority-dev/blurb/actions/workflows/ci.yml/badge.svg)](https://github.com/superiority-dev/blurb/actions/workflows/ci.yml) [![Coverage](https://codecov.io/gh/superiority-dev/blurb/graph/badge.svg)](https://codecov.io/gh/superiority-dev/blurb)

A production-ready service that turns an image into the placeholder you choose
for a page to show while the image loads:
[BlurHash](https://github.com/superiority-dev/blurhash),
[ThumbHash](https://github.com/superiority-dev/thumbhash), LQIP or average color.

Two ways to use it:

- **Store at upload.** Your backend calls blurb once per upload and stores the
  fields it uses beside the image. Every page view after that reads the
  placeholder from your database, with no further call.
- **Request per view through a CDN.** Pages call `GET /hash?url=...`, and the
  CDN caches each successful response for `BLURB_MAX_AGE` (8760h by
  default). The placeholder arrives in its own response, after the page. A
  request for a URL the CDN has not cached yet waits for blurb to fetch and
  hash the image. blurb sends no CORS headers, so a page on another origin
  needs the CDN to add them.

| Image | `lqip` | `thumbhash` | `blurhash` | `color` |
|---|---|---|---|---|
| <img src=".github/example.jpg" width="120" alt="A heron on a riverbank, reflected in the water"> | <img src=".github/example-lqip.png" width="120" alt="The lqip preview, scaled up"> | <img src=".github/example-thumbhash.png" width="120" alt="The ThumbHash, decoded"> | <img src=".github/example-blurhash.png" width="120" alt="The BlurHash, decoded"> | <img src=".github/example-color.png" width="120" alt="The image's average color"> |

```
$ curl --data-binary @photo.jpg http://localhost:8080/hash
{"blurhash":"LKECXuIUV@M{~DafjsNGInt7WUoz","thumbhash":"WggKDYRklVd/aHiWlzd2VniAaPhZ","width":360,"height":271,"aspect":1.3284,"format":"jpeg","bytes":23920,"has_alpha":false,"orientation":1,"color":"#6f6a62"}
```

## Features

- **Response**
  - by default: fields shown above
  - on request: tiny PNG preview (`lqip`), dominant colors, perceptual hash, SHA-256
- **Setup**
  - builds to a single binary
  - one required variable
  - no database, no state on disk
- **Input**
  - `POST` image as body, or `GET` it by URL
  - JPEG, PNG, GIF and WebP, detected from bytes
  - EXIF orientation applied for JPEG and PNG, so size and hashes
    match displayed image
  - animated GIF and WebP: first frame
- **Memory management**
  - pixel limit checked from image header, before decoding
  - decode starts only when its estimated memory, plus that of running
    decodes, fits in 75% of memory limit
  - overload: `503` with `Retry-After: 1`
- **SSRF protection**
  - `GET` fetches only from allowed hosts
  - checks resolved address and refuses loopback, private and link-local
    ones, such as cloud metadata `169.254.169.254`
  - every redirect checked like first request
- **Operations**
  - `/healthz` and `/readyz`
  - JSON logs to standard output
  - OpenTelemetry metrics over OTLP/HTTP
  - graceful shutdown on `SIGTERM`

## Install

Run the image from Docker Hub:

```
docker run --rm -p 127.0.0.1:8080:8080 --memory 512m \
  -e BLURB_ALLOWED_HOSTS=images.example.com superioritydev/blurb
```

`docker compose -f deploy/compose.yaml up` runs it for `POST` only, with a
read-only filesystem and no capabilities.

On Kubernetes, install the Helm chart:

```
helm install blurb oci://registry-1.docker.io/superioritydev/blurb-chart \
  --set 'allowedHosts={images.example.com}' --set resources.limits.memory=512Mi
```

The full chart is in [deploy/helm](deploy/helm).

The image and the chart are also on GitHub Container Registry, as
`ghcr.io/superiority-dev/blurb` and
`oci://ghcr.io/superiority-dev/charts/blurb-chart`.

Binaries for Linux, macOS and Windows, on amd64 and arm64, are on the
[releases page](https://github.com/superiority-dev/blurb/releases).

## Usage

`BLURB_ALLOWED_HOSTS` names hosts `GET` may fetch images from. It is
required. If you only `POST`, give a name that never resolves, such as
`none.invalid`. Set a memory limit too, with `GOMEMLIMIT` or the container's.
The lower one counts. Without either, memory is not budgeted.

```
BLURB_ALLOWED_HOSTS=images.example.com GOMEMLIMIT=1GiB ./blurb
```

blurb listens on port 8080 on every interface and has no authentication:
anyone who reaches it can have images decoded. Check that it is up:

```
$ curl http://localhost:8080/readyz
{"status":"ok"}
```

`POST` takes image bytes as request body, with any `Content-Type`. `GET`
takes a URL in `url`. Both return the same JSON fields. `-G --data-urlencode`
encodes a `url` that contains a query string:

```
curl -G --data-urlencode "url=https://images.example.com/photo.jpg?w=800&q=80" http://localhost:8080/hash
```

## Showing a placeholder

In this React component, `record` is the response, stored or fetched, from
`/hash?formats=lqip,thumbhash,blurhash`.
`width` and `height` reserve image space and `color` fills it until the image
loads:

```jsx
function Photo({ src, record }) {
  const background = record.color;
  return (
    <img src={src} width={record.width} height={record.height}
         style={{ background }}
         ref={(img) => { if (img?.complete) img.style.background = ""; }}
         onLoad={(e) => (e.currentTarget.style.background = "")} />
  );
}
```

`onLoad` clears the background once the image loads, so the placeholder does
not show through transparent pixels. `ref` clears it for an image that loaded
before React ran, as with server rendering.

Each subsection below replaces the `background` line in `Photo`. Its `import`
goes at the top of the file.

### lqip

A PNG data URI that needs no decoding:

```jsx
const background = `center / cover url(${record.lqip})`;
```

### thumbhash

Decoded with [thumbhash](https://www.npmjs.com/package/thumbhash)
(`npm install thumbhash`):

```jsx
import { thumbHashToDataURL } from "thumbhash";

const bytes = Uint8Array.from(atob(record.thumbhash), (c) => c.charCodeAt(0));
const background = `center / cover url(${thumbHashToDataURL(bytes)})`;
```

### blurhash

Decoded with [blurhash](https://www.npmjs.com/package/blurhash)
(`npm install blurhash`) into 32 by 32 pixels. A canvas turns them into a data
URI, so this needs `document` and does not run under server rendering:

```jsx
import { decode } from "blurhash";

const canvas = document.createElement("canvas");
canvas.width = canvas.height = 32;
const context = canvas.getContext("2d");
const pixels = context.createImageData(32, 32);
pixels.data.set(decode(record.blurhash, 32, 32));
context.putImageData(pixels, 0, 0);
const background = `center / 100% 100% url(${canvas.toDataURL()})`;
```

## Configuration

All settings are environment variables. An invalid value stops startup, and
all problems are logged to standard output together.

| Variable | Default | Example | Description |
|---|---|---|---|
| `BLURB_ALLOWED_HOSTS` | required | `images.example.com,*.cdn.example.com` | hosts `GET` may fetch from: `*.example.com` matches hosts under `example.com` but not `example.com` itself, and `*` matches any host |
| `BLURB_ADDR` | `:8080` | `127.0.0.1:9000` | listen address, where empty host means every interface |
| `BLURB_PATH_PREFIX` | none | `/v1` | serves `/hash` at `/v1/hash` and leaves health paths where they are |
| `BLURB_BASE_URL` | none | `https://images.example.com/` | lets `url` be relative to it, and its host must be in `BLURB_ALLOWED_HOSTS` too |
| `BLURB_FOLLOW_REDIRECTS` | `true` | `false` | follow up to 5 redirects, and with `false` a redirect gets `502` |
| `BLURB_ALLOW_PRIVATE_ADDRESSES` | `false` | `true` | allow fetches from any address, loopback, private and link-local included, for image server on your own network |
| `BLURB_FETCH_TIMEOUT` | `5s` | `10s` | time limit for fetch, and for reading `POST` body |
| `BLURB_MAX_DOWNLOAD_BYTES` | `128MiB` | `32MiB` | largest image in bytes, fetched or posted |
| `BLURB_MAX_MEGAPIXELS` | `50` | `12.5` | largest image in pixels, width x height, in millions |
| `BLURB_MAX_CONCURRENT_DECODES` | `0` | `8` | decodes running together, where `0` means `GOMAXPROCS` |
| `BLURB_MAX_WAITING_REQUESTS` | 4 per concurrent decode | `64` | requests that may wait for decode, where `0` means none wait |
| `BLURB_QUEUE_TIMEOUT` | `5s` | `2s` | longest wait for decode, then `503` |
| `BLURB_MAX_AGE` | `8760h` | `24h` | how long cache or CDN in front of blurb may keep successful `GET` response, and `0s` turns caching off |
| `BLURB_REQUEST_ID_HEADER` | `X-Request-Id` | `X-Correlation-Id` | header with request id: kept from request when it has 1 to 128 visible ASCII characters, generated otherwise, sent back and logged |
| `BLURB_BLURHASH_COMPONENTS` | `4x3` | `5x4` | BlurHash components across and down, 1 to 9 each |
| `BLURB_LOG_LEVEL` | `info` | `debug` | `debug`, `info`, `warn` or `error` |
| `BLURB_SHUTDOWN_TIMEOUT` | `25s` | `30s` | time to finish requests after `SIGTERM` |

Metrics are off by default:

| Variable | Example | Description |
|---|---|---|
| `OTEL_METRICS_EXPORTER` | `otlp` | `otlp` exports, and unset or `none` is off |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://otel-collector:4318` | collector address |
| `OTEL_SERVICE_NAME` | `blurb-images` | service name, `blurb` by default |

## API

### Query parameters

| Parameter | Values | Default | Example |
|---|---|---|---|
| `url` | `http` or `https` URL, relative only with `BLURB_BASE_URL`, `GET` only | required for `GET` | `url=https://images.example.com/photo.jpg` |
| `formats` | `blurhash`, `thumbhash`, `lqip`, comma-separated | `blurhash,thumbhash` | `formats=thumbhash,lqip` |
| `extras` | `dominant_colors`, `phash`, `sha256`, comma-separated | none | `extras=phash,sha256` |

```
$ curl --data-binary @photo.jpg "http://localhost:8080/hash?formats=thumbhash,lqip&extras=dominant_colors,phash,sha256"
{"thumbhash":"WggKDYRklVd/aHiWlzd2VniAaPhZ","lqip":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAoAAAAICAIAAABPmPnhAAABBUlEQVR4nAD4AAf/BDwzNB4dHAcHBg4NChUQExAPDvf6+Onr7wcGBgMCAARWS0kJCAj+/v4LCgcIBgPa7ffk9QwoGAUfGRT+/QACBwgLCg8ZDA4V+gAN8voIA/r15Ne9p7S2AQYOAwgNA+fv5fX49fP5/gAFBRAWGQ4RGxsSDfv/ARkaJvX4+wQDBfnj38nt79oA2f4bICcXDg3v7OodGSUDAwDw8vcBTWBY/vLrBggEAf7//fjzBP39Dwb/9fv/Bw4SA/wBAxgaDQsUFPn4+Pz29Q4TFhoWIfX6A/sFCPsFA+zp6wMfJBj+AfgFAwYDBwj7Av/3/AL+BgkBBgP6/vkC/wEDAGamYwxx9lVPAAAAAElFTkSuQmCC","width":360,"height":271,"aspect":1.3284,"format":"jpeg","bytes":23920,"has_alpha":false,"orientation":1,"color":"#6f6a62","dominant_colors":[{"color":"#464937","share":0.06973333333333333},{"color":"#4b5846","share":0.04493333333333333},{"color":"#565948","share":0.0372},{"color":"#998787","share":0.03626666666666667},{"color":"#b59c96","share":0.0272}],"phash":"89ad56161a496f3d","sha256":"2d6853cca81941213927e2062012f4f4dce7fdb8d1c39549af93389066c6adb2"}
```

### Response fields

| Field | Type | Example | Description |
|---|---|---|---|
| `blurhash` | string | `"LKECXuIUV@M{~DafjsNGInt7WUoz"` | placeholder in BlurHash format |
| `thumbhash` | string | `"WggKDYRklVd/aHiWlzd2VniAaPhZ"` | placeholder in ThumbHash format, standard padded base64 |
| `lqip` | string | `"data:image/png;base64,..."` | image as PNG of at most 10x10, as data URI |
| `width`, `height` | number | `360`, `271` | size as displayed, after EXIF orientation |
| `aspect` | number | `1.3284` | `width` / `height`, 4 decimals, for CSS `aspect-ratio` |
| `format` | string | `"jpeg"` | `jpeg`, `png`, `gif` or `webp` |
| `bytes` | number | `23920` | input size in bytes |
| `has_alpha` | bool | `false` | `true` when image may have transparent pixels: alpha channel, transparent palette color, or first frame smaller than its canvas |
| `orientation` | number | `1` | EXIF orientation found, 1 to 8, or `1` when none |
| `color` | string | `"#6f6a62"` | average of visible pixels, weighted by opacity |
| `dominant_colors` | array | `[{"color":"#464937","share":0.0697...}]` | up to 5 colors, each the average of one of the 5 largest of 4096 color bins, with its share of visible pixels |
| `phash` | string | `"89ad56161a496f3d"` | 64-bit perceptual hash in hex, compared between two images by counting differing bits |
| `sha256` | string | `"2d6853cc..."` | SHA-256 of input bytes, hex |

### Errors

```
$ curl "http://localhost:8080/hash?url=https://other.example/a.jpg"
{"error":{"code":"host_not_allowed","message":"the image host is not allowed"}}
```

| Status | `code` | When |
|---|---|---|
| 400 | `bad_request` | parameter is missing, has unknown value or is given twice, or `POST` body ends early or arrives broken, such as bad chunked encoding |
| 403 | `host_not_allowed` | host is not in `BLURB_ALLOWED_HOSTS`, or resolves to a refused address |
| 404 | `not_found` | no such path |
| 404 | `upstream_not_found` | image server answered `404` or `410` |
| 405 | `method_not_allowed` | method is not accepted on path |
| 408 | `body_timeout` | `POST` body took longer than `BLURB_FETCH_TIMEOUT` |
| 413 | `too_large` | over `BLURB_MAX_DOWNLOAD_BYTES` or `BLURB_MAX_MEGAPIXELS`, or needs more memory than blurb may use even when idle, so a retry fails too |
| 415 | `unsupported_media_type` | not JPEG, PNG, GIF or WebP, or uses feature the decoder lacks |
| 422 | `unreadable_image` | image is empty or corrupt |
| 500 | `internal` | bug in blurb |
| 502 | `upstream_failed` | image server could not be reached, sent broken body, answered another status, or redirected more than 5 times or to URL blurb does not fetch |
| 503 | `unavailable` | too many requests at once or wait for decode timed out, sent with `Retry-After: 1` |
| 503 | `not_ready` | `/readyz` while blurb is starting or shutting down |
| 504 | `upstream_timeout` | image server took longer than `BLURB_FETCH_TIMEOUT` |

### Health

| Path | `200` | `503` |
|---|---|---|
| `/healthz` | process is running | never |
| `/readyz` | ready to serve | starting or shutting down |

## Limitations

- No HEIC or AVIF.
- The whole image is decoded before it is scaled down, so a large image needs
  memory in proportion to its pixels. `BLURB_MAX_MEGAPIXELS` caps the pixel
  count.
- No Prometheus `/metrics` endpoint. Metrics are OTLP only.
