#!/usr/bin/env bash
#
# Bring up a throwaway Emby or Jellyfin in Docker, lay out a small made-up
# catalogue for it (films, shows, music, music videos, home videos and photos,
# books and an audiobook), give it the Playback Reporting plugin, complete its
# setup wizard, mint an API key, and print the environment the live tests need.
#
#   eval "$(EMBYFIN_EXPORTER_TEST_BACKEND=jellyfin .scripts/testenv.sh up)"   # start, export EMBYFIN_EXPORTER_*
#   EMBYFIN_EXPORTER_TEST_BACKEND=jellyfin .scripts/testenv.sh down            # stop and remove everything
#   .scripts/testenv.sh fixtures                                              # write the media tree only
#
# EMBYFIN_EXPORTER_TEST_ONLINE=1 with up gives the server a way out to the
# internet, for the one test that needs it to reach its plugin catalogue.
#
# This script only does what the tests cannot do through the server's API:
# run the container, put the plugin in it, get through the first-run wizard,
# create the users, mint the API key, and lay the media out on disk. The libraries, the collection
# and the playlist are the test suite's job (acceptance/harness_test.go), and
# the counts it expects are of the catalogue below: change one and the other
# must agree.
#
# The server is given a proxy nothing listens on, so nothing it would ask of
# the internet on starting, or for a film's metadata, leaves the container.
# The plugin is fetched by this script and not by the server for that reason.
# It is embyfin-mcp's .scripts/testenv.sh cut down to what a program that
# only reads needs.

set -euo pipefail

BACKEND="${EMBYFIN_EXPORTER_TEST_BACKEND:-}"
case "$BACKEND" in
  emby)
    IMAGE="${EMBYFIN_EXPORTER_TEST_IMAGE:-emby/embyserver:latest}"
    PORT="${EMBYFIN_EXPORTER_TEST_PORT:-19097}"
    PLUGIN="${EMBYFIN_EXPORTER_TEST_PLUGIN:-https://embydata.com/admin/service/packageFiles/playback_reporting.dll_2.1.0.7.exe}"
    ;;
  jellyfin)
    IMAGE="${EMBYFIN_EXPORTER_TEST_IMAGE:-jellyfin/jellyfin:latest}"
    PORT="${EMBYFIN_EXPORTER_TEST_PORT:-19096}"
    PLUGIN="${EMBYFIN_EXPORTER_TEST_PLUGIN:-https://repo.jellyfin.org/files/plugin/playback-reporting/playback-reporting_19.0.0.0.zip}"
    ;;
  "")
    if [ "${1:-up}" != "fixtures" ]; then
      echo "EMBYFIN_EXPORTER_TEST_BACKEND must be emby or jellyfin" >&2
      exit 1
    fi
    ;;
  *)
    echo "EMBYFIN_EXPORTER_TEST_BACKEND=$BACKEND: want emby or jellyfin" >&2
    exit 1
    ;;
esac

NAME="${EMBYFIN_EXPORTER_TEST_CONTAINER:-embyfin-exporter-test-${BACKEND}}"
# not TMPDIR: on macOS that is /var/folders/..., which Docker does not share
# by default, and the bind mounts silently come up empty
DATA="${EMBYFIN_EXPORTER_TEST_DATA:-${HOME}/.cache/embyfin-exporter/testenv/${BACKEND:-fixtures}}"
# the names are ones no reading's name or help holds, so a test can look for
# them on the page and be sure of what it finds
ADMIN="wendy"
PASSWORD="embyfin-exporter-test"
# a second, ordinary account, for the tests to log a device in as
USER2="alice"
# no backend means "fixtures", which needs no server: leave the port empty
URL="http://127.0.0.1:${PORT:-}"

log() { echo "==> $*" >&2; }

# api METHOD PATH [BODY] [TOKEN] - curl against the test server, failing loudly.
# The token goes in the header both servers read.
api() {
  local method=$1 path=$2 body=${3:-} token=${4:-}
  local args=(-fsS -X "$method" "${URL}${path}" -H 'Content-Type: application/json')
  [ -n "$token" ] && args+=(-H "X-Emby-Token: ${token}" -H "Authorization: MediaBrowser Token=\"${token}\", Client=\"embyfin-exporter-testenv\", Device=\"testenv\", DeviceId=\"testenv\", Version=\"0\"")
  [ -n "$body" ] && args+=(-d "$body")
  curl "${args[@]}"
}

# ---------------------------------------------------------------------------
# The catalogue: 4 films, 2 series with 3 seasons and 5 episodes between
# them, 2 artists with 3 albums and 6 tracks, 2 music videos, a folder of
# 1 home video and 2 photos, 2 books, 1 audiobook in 2 chapters, and a
# folder that mixes 1 film with 1 series of 1 episode. The servers are never
# asked to look any of it up, so the names only have to be laid out the way
# each server reads a folder.

# folder
FILMS='Alien (1979)
Arrival (2016)
Blade Runner (1982)
Dune (2021)'

# show folder|season|episode
EPISODES='Severance|1|1
Severance|1|2
Severance|2|1
The Expanse|1|1
The Expanse|1|2'

# artist|album|year|track number|title - a song is read from its tags, which
# are all the servers know of it
TRACKS='Pink Floyd|The Dark Side of the Moon|1973|1|Speak to Me
Pink Floyd|The Dark Side of the Moon|1973|2|Breathe
Pink Floyd|Wish You Were Here|1975|1|Shine On You Crazy Diamond
Pink Floyd|Wish You Were Here|1975|2|Welcome to the Machine
Battle Tapes|Polygon|2015|1|Belgrade
Battle Tapes|Polygon|2015|2|Valkyrie'

# file
MUSIC_VIDEOS='Daft Punk - Around the World
Gorillaz - Clint Eastwood'

# photo in the one folder of home videos, which also holds one clip
PHOTOS='one
two'

# author|title
BOOKS='Ursula K. Le Guin|The Dispossessed
Stanislaw Lem|Solaris'

# author|book|chapter number|title - read from its tags, as a song is
CHAPTERS='Frank Herbert|Dune|1|Arrakis
Frank Herbert|Dune|2|Muad Dib'

# the folder that mixes kinds: one film, and one series of one episode
MIXED_FILM='Heat (1995)'
MIXED_EPISODE='Dark|1|1'

# wipe_data removes the data directory. The container writes its config,
# metadata and cache as its own user, and on Linux those land in the bind
# mount owned by someone the calling user is not, so cannot delete them. A
# throwaway root container always can.
wipe_data() {
  [ -d "${DATA}" ] || return 0
  rm -rf "${DATA}" 2>/dev/null && return 0

  log "removing container-owned files"
  docker run --rm -v "${DATA}:/data" mirror.gcr.io/library/alpine:3 sh -c 'rm -rf /data/..?* /data/.[!.]* /data/*' >/dev/null 2>&1 ||
    docker run --rm -v "${DATA}:/data" alpine:3 sh -c 'rm -rf /data/..?* /data/.[!.]* /data/*' >/dev/null 2>&1 || true
  rm -rf "${DATA}" 2>/dev/null || true
}

# clip FILE - two seconds of a moving test pattern with a tone under it, so
# the scanner has a real file to probe and a server a real one to transcode.
# -nostdin matters: without it ffmpeg reads the while-read loop's stdin and
# swallows a character of the next line.
clip() {
  mkdir -p "$(dirname "$1")"
  ffmpeg -nostdin -loglevel error -y -f lavfi -i "testsrc2=s=640x360:r=24" -f lavfi -i "sine=frequency=440:sample_rate=44100" -t 2 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$1"
}

# song FILE TITLE TRACK ARTIST ALBUM YEAR - a one-second tone tagged as the
# track it stands for.
song() {
  mkdir -p "$(dirname "$1")"
  ffmpeg -nostdin -loglevel error -y -f lavfi -i "sine=frequency=$((220 + $3 * 55)):duration=1:sample_rate=44100" -c:a libmp3lame -b:a 64k -id3v2_version 3 -metadata "title=$2" -metadata "track=$3" -metadata "artist=$4" -metadata "album_artist=$4" -metadata "album=$5" -metadata "date=$6" "$1"
}

# still FILE - one frame of the test pattern, as a photo.
still() {
  mkdir -p "$(dirname "$1")"
  ffmpeg -nostdin -loglevel error -y -f lavfi -i "testsrc2=s=320x240" -frames:v 1 "$1"
}

# pdf FILE - the least a file can hold and still be a PDF of one blank page.
# A book is known by its file's name and kind; neither server opens it to
# count it.
pdf() {
  mkdir -p "$(dirname "$1")"
  printf '%%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\ntrailer<</Root 1 0 R>>\n%%%%EOF\n' > "$1"
}

fixtures() {
  log "generating fixtures under ${DATA}"
  wipe_data
  mkdir -p "${DATA}/media" "${DATA}/config"

  # one clip, made once and copied to every film and episode
  local master="${DATA}/clip.mkv"
  clip "$master"

  local film
  while IFS= read -r film; do
    mkdir -p "${DATA}/media/films/${film}"
    cp "$master" "${DATA}/media/films/${film}/${film}.mkv"
  done <<EOF
${FILMS}
EOF

  local show season ep dir
  while IFS='|' read -r show season ep; do
    dir="${DATA}/media/shows/${show}/Season $(printf '%02d' "$season")"
    mkdir -p "$dir"
    cp "$master" "${dir}/${show} - S$(printf '%02d' "$season")E$(printf '%02d' "$ep").mkv"
  done <<EOF
${EPISODES}
EOF

  local artist album year track title
  while IFS='|' read -r artist album year track title; do
    song "${DATA}/media/music/${artist}/${album}/$(printf '%02d' "$track") - ${title}.mp3" "$title" "$track" "$artist" "$album" "$year"
  done <<EOF
${TRACKS}
EOF

  local video
  while IFS= read -r video; do
    mkdir -p "${DATA}/media/musicvideos"
    cp "$master" "${DATA}/media/musicvideos/${video}.mkv"
  done <<EOF
${MUSIC_VIDEOS}
EOF

  local photo
  mkdir -p "${DATA}/media/home/Holiday"
  cp "$master" "${DATA}/media/home/Holiday/beach.mkv"
  while IFS= read -r photo; do
    still "${DATA}/media/home/Holiday/${photo}.jpg"
  done <<EOF
${PHOTOS}
EOF

  local author book chapter
  while IFS='|' read -r author book; do
    pdf "${DATA}/media/books/${author}/${book}/${book}.pdf"
  done <<EOF
${BOOKS}
EOF

  while IFS='|' read -r author book chapter title; do
    song "${DATA}/media/audiobooks/${author}/${book}/$(printf '%02d' "$chapter") - ${title}.mp3" "$title" "$chapter" "$author" "$book" 1965
  done <<EOF
${CHAPTERS}
EOF

  mkdir -p "${DATA}/media/mixed/${MIXED_FILM}"
  cp "$master" "${DATA}/media/mixed/${MIXED_FILM}/${MIXED_FILM}.mkv"
  IFS='|' read -r show season ep <<EOF
${MIXED_EPISODE}
EOF
  dir="${DATA}/media/mixed/${show}/Season $(printf '%02d' "$season")"
  mkdir -p "$dir"
  cp "$master" "${dir}/${show} - S$(printf '%02d' "$season")E$(printf '%02d' "$ep").mkv"

  rm -f "$master"
}

# plugin lays the Playback Reporting plugin where the server looks for
# plugins when it starts: the readings of what was played come from it. The
# address is the one each server's own catalogue gives for the version, and
# a Jellyfin one is built for one version of Jellyfin: when the server's image
# moves on, name the newer plugin in EMBYFIN_EXPORTER_TEST_PLUGIN or above.
# Emby's is one file; Jellyfin's is a folder of them, zipped.
plugin() {
  log "fetching the Playback Reporting plugin"
  local dir="${DATA}/config/plugins"
  mkdir -p "$dir"
  if [ "$BACKEND" = "emby" ]; then
    curl -fsSL -o "${dir}/playback_reporting.dll" "$PLUGIN"
  else
    curl -fsSL -o "${DATA}/plugin.zip" "$PLUGIN"
    unzip -q -o "${DATA}/plugin.zip" -d "${dir}/Playback Reporting"
    rm -f "${DATA}/plugin.zip"
  fi
  # the server runs as a user of its own and writes beside what it loads
  chmod -R a+rwX "$dir"
}

# pull_image fetches the server's image when this machine does not have it:
# from Google's mirror of Docker Hub, which holds the same image under the
# same name, and from Docker Hub itself when the mirror does not give it.
# Docker Hub limits how much an address may pull and a CI runner shares its
# address, so it is the one asked second. An image already here is used as
# it is: docker pull it to test a newer one.
pull_image() {
  docker image inspect "$IMAGE" >/dev/null 2>&1 && return 0
  case "$IMAGE" in
    */*/*)
      # names a registry of its own: there is no mirror of it to try
      docker pull -q "$IMAGE" >/dev/null && return 0
      echo "could not pull ${IMAGE}" >&2
      exit 1
      ;;
  esac
  if docker pull -q "mirror.gcr.io/${IMAGE}" >/dev/null 2>&1; then
    docker tag "mirror.gcr.io/${IMAGE}" "$IMAGE"
    return 0
  fi
  log "mirror.gcr.io would not give ${IMAGE}: taking it from Docker Hub"
  docker pull -q "$IMAGE" >/dev/null || {
    echo "could not pull ${IMAGE} from mirror.gcr.io or from Docker Hub" >&2
    exit 1
  }
}

# retry TRIES COMMAND... - run a command until it succeeds, for the wizard
# steps: Jellyfin answers its first requests with a 404 or 503 for a moment
# after it starts listening, while the rest of the app comes up
retry() {
  local tries=$1
  shift
  for _ in $(seq "$tries"); do
    if "$@"; then return 0; fi
    sleep 2
  done
  echo "giving up on: $*" >&2
  return 1
}

# wait_for WHAT TRIES COMMAND
wait_for() {
  local what=$1 tries=$2 cmd=$3
  log "waiting for ${what}"
  for _ in $(seq "$tries"); do
    if eval "$cmd" >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  echo "timed out waiting for ${what}" >&2
  docker logs "$NAME" 2>&1 | tail -40 >&2
  return 1
}

# logs prints what the server itself wrote. Emby logs to
# /config/logs/embyserver.txt rather than stdout, so `docker logs` shows
# almost nothing of what went wrong; Jellyfin writes /config/log/*.log.
logs() {
  echo "==> docker logs ${NAME}" >&2
  docker logs "$NAME" 2>&1 | tail -40 >&2 || true
  for f in "${DATA}"/config/logs/*.txt "${DATA}"/config/log/*.log; do
    [ -f "$f" ] || continue
    echo "==> ${f} (errors)" >&2
    # a file with none of these is no failure: one that ended this script would
    # leave the caller's teardown undone, and the server running
    grep -iE 'error|warn|exception|cancel|refused|timed out' "$f" | tail -60 >&2 || true
    echo "==> ${f} (tail)" >&2
    tail -40 "$f" >&2
    # a log may end without a newline, which would run the next heading onto its last line
    echo >&2
  done
}

up() {
  command -v ffmpeg >/dev/null || { echo "ffmpeg is required to generate fixtures" >&2; exit 1; }
  command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
  command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
  command -v unzip >/dev/null || { echo "unzip is required" >&2; exit 1; }

  down >/dev/null 2>&1 || true
  fixtures
  plugin
  pull_image

  log "starting ${IMAGE} as ${NAME} on ${PORT}"
  local mounts=(-v "${DATA}/media:/media" -v "${DATA}/config:/config")
  [ "$BACKEND" = "jellyfin" ] && mounts+=(-v "${DATA}/cache:/cache")
  # NO_PROXY carries the container's own name: Emby pings itself over HTTP
  # on startup, and that has no business going to a proxy that is not there
  local proxy=(-e "HTTP_PROXY=http://127.0.0.1:9" -e "HTTPS_PROXY=http://127.0.0.1:9" -e "http_proxy=http://127.0.0.1:9" -e "https_proxy=http://127.0.0.1:9" -e "NO_PROXY=localhost,127.0.0.1,${NAME}")
  # EMBYFIN_EXPORTER_TEST_ONLINE leaves the proxy out, for the one test that
  # needs the server to reach its plugin catalogue. It is never set by make
  [ -n "${EMBYFIN_EXPORTER_TEST_ONLINE:-}" ] && proxy=()
  docker run -d --name "$NAME" -p "127.0.0.1:${PORT}:8096" --hostname "$NAME" ${proxy[@]+"${proxy[@]}"} "${mounts[@]}" "$IMAGE" >/dev/null

  # /System/Info/Public answers before the wizard endpoints do, so wait on
  # the first endpoint the setup actually uses
  wait_for "the server to answer /Startup/User" 90 "curl -fsS ${URL}/Startup/User"

  # the first-run wizard, the way the web client drives it: locale, the
  # first user, then Complete. Emby creates the first user before the wizard
  # and POST /Startup/User renames it; Jellyfin creates it from the same call.
  log "completing the setup wizard"
  retry 15 api POST /Startup/Configuration '{"UICulture":"en-US","MetadataCountryCode":"US","PreferredMetadataLanguage":"en"} ' >/dev/null
  retry 15 api POST /Startup/User "$(jq -n --arg u "$ADMIN" --arg p "$PASSWORD" '{Name: $u, Password: $p}')" >/dev/null
  if [ "$BACKEND" = "jellyfin" ]; then
    retry 15 api POST /Startup/RemoteAccess '{"EnableRemoteAccess":true,"EnableAutomaticPortMapping":false}' >/dev/null
  fi
  retry 15 api POST /Startup/Complete >/dev/null

  log "logging in"
  local login access key
  login=$(curl -fsS -X POST "${URL}/Users/AuthenticateByName" -H 'Content-Type: application/json' -H 'X-Emby-Authorization: MediaBrowser Client="embyfin-exporter-testenv", Device="testenv", DeviceId="testenv", Version="0"' -H 'Authorization: MediaBrowser Client="embyfin-exporter-testenv", Device="testenv", DeviceId="testenv", Version="0"' -d "$(jq -n --arg u "$ADMIN" --arg p "$PASSWORD" '{Username: $u, Pw: $p}')")
  access=$(echo "$login" | jq -r '.AccessToken')
  [ "$access" != "null" ] || { echo "no AccessToken in the login response" >&2; exit 1; }

  # an API key is what embyfin-exporter is meant to run on, so the tests
  # authenticate the way a deployment does rather than with a session token
  log "creating an API key"
  api POST "/Auth/Keys?app=embyfin-exporter-test" "" "$access" >/dev/null
  key=$(api GET /Auth/Keys "" "$access" | jq -r '.Items[] | select(.AppName == "embyfin-exporter-test") | .AccessToken' | head -1)
  [ -n "$key" ] && [ "$key" != "null" ] || { echo "no API key in the response" >&2; exit 1; }

  log "creating user ${USER2}"
  local u2
  if [ "$BACKEND" = "jellyfin" ]; then
    u2=$(api POST /Users/New "$(jq -n --arg u "$USER2" --arg p "$PASSWORD" '{Name: $u, Password: $p}')" "$key" | jq -r .Id)
  else
    u2=$(api POST /Users/New "$(jq -n --arg u "$USER2" '{Name: $u}')" "$key" | jq -r .Id)
    api POST "/Users/${u2}/Password" "$(jq -n --arg p "$PASSWORD" '{NewPw: $p}')" "$key" >/dev/null
  fi
  [ -n "$u2" ] && [ "$u2" != "null" ] || { echo "could not create ${USER2}" >&2; exit 1; }

  # consumed with eval "$(.scripts/testenv.sh up)"
  echo "export EMBYFIN_EXPORTER_TEST_BACKEND='${BACKEND}'"
  echo "export EMBYFIN_EXPORTER_TEST_SERVER='${URL}'"
  echo "export EMBYFIN_EXPORTER_TEST_TOKEN='${key}'"
  echo "export EMBYFIN_EXPORTER_TEST_ADMIN='${ADMIN}'"
  echo "export EMBYFIN_EXPORTER_TEST_USER='${USER2}'"
  echo "export EMBYFIN_EXPORTER_TEST_USER_ID='${u2}'"
  echo "export EMBYFIN_EXPORTER_TEST_PASSWORD='${PASSWORD}'"
  # for the tests that stop the server answering, or run the program's own image beside it
  echo "export EMBYFIN_EXPORTER_TEST_CONTAINER='${NAME}'"
  # for the test that needs the server to have reached its plugin catalogue
  echo "export EMBYFIN_EXPORTER_TEST_ONLINE='${EMBYFIN_EXPORTER_TEST_ONLINE:-}'"
}

down() {
  log "removing ${NAME}"
  # and the program's own image, which one test runs beside the server and
  # removes itself unless it was cut short
  docker rm -f "$NAME" "${NAME}-program" >/dev/null 2>&1 || true
  wipe_data
}

case "${1:-up}" in
  up) up ;;
  down) down ;;
  fixtures) fixtures ;;   # generate the media tree only, for inspecting the layout
  logs) logs ;;           # what the server wrote about itself, for a failing run
  image) echo "$IMAGE" ;; # the image this backend runs, for a docker pull
  *) echo "usage: EMBYFIN_EXPORTER_TEST_BACKEND=emby|jellyfin $0 [up|down|fixtures|logs|image]" >&2; exit 1 ;;
esac
