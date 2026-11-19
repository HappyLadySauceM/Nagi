#!/usr/bin/env bash
set -euo pipefail

: "${IMAGES:?}" "${RELEASE_DIR:?}" "${SHORT_SHA:?}" "${IMAGE_VERSION:?}" "${ZOT:?}"
read -r -a images <<< "$IMAGES"
# Main releases share one concurrency group. Reuse the temporary tag so old
# versions return to having one SHA tag and remain eligible for retention.
stage_tag=staging

stage_images() {
  umask 077
  cosign_key=$(mktemp "$RELEASE_DIR/cosign.XXXXXX")
  trap 'unlink -- "$cosign_key"' EXIT
  printf '%s' "${COSIGN_KEY:?}" > "$cosign_key"
  export COSIGN_PASSWORD

  for image in "${images[@]}"; do
    local context args started signed digest cache_digest
    args=()
    case "$image" in
      api)
        context=.
        args+=(--build-context "nagi_artifacts=$RELEASE_DIR/api"
               --build-arg "VERSION=$IMAGE_VERSION" --build-arg "COMMIT=$SHORT_SHA")
        ;;
      web)
        context=web
        test -s "$PWD/web/dist/index.html"
        args+=(--build-context "nagi_artifacts=$PWD/web/dist")
        ;;
      *)
        echo "未知镜像 $image" >&2
        return 1
        ;;
    esac

    local zot_image="$ZOT/nagi/$image"
    local ghcr_image="ghcr.io/happyladysaucem/nagi-$image"
    local cache="$ZOT/buildcache/nagi-$image"

    started=$SECONDS
    docker buildx build --platform linux/amd64 --target package-only --provenance=false --push \
      --cache-from "type=registry,ref=$cache:main" \
      --cache-from "type=registry,ref=$cache:package" \
      --cache-to "type=registry,ref=$cache:package,mode=min,image-manifest=true,oci-mediatypes=true" \
      --label "org.opencontainers.image.title=Nagi-$image" \
      --label "org.opencontainers.image.version=$IMAGE_VERSION" \
      --label "org.opencontainers.image.revision=$GITHUB_SHA" \
      --label "org.opencontainers.image.source=$GITHUB_SERVER_URL/$GITHUB_REPOSITORY" \
      --tag "$zot_image:$stage_tag" --tag "$ghcr_image:$stage_tag" "${args[@]}" "$context"
    echo "$image 包装与推送耗时 $((SECONDS - started)) 秒"

    signed=$SECONDS
    digest=$(crane digest "$zot_image:$stage_tag")
    test "$digest" = "$(crane digest "$ghcr_image:$stage_tag")"
    cache_digest=$(crane digest "$cache:package")
    cosign sign --key "$cosign_key" --yes --tlog-upload=false "$zot_image@$digest"
    cosign sign --key "$cosign_key" --yes --tlog-upload=false "$cache@$cache_digest"
    printf '%s\n' "$digest" > "$RELEASE_DIR/$image.digest"
    echo "$image 摘要校验与签名耗时 $((SECONDS - signed)) 秒"
  done
}

promote_images() {
  # All requested images must have been staged and signed before any formal tag is written.
  for image in "${images[@]}"; do
    test -s "$RELEASE_DIR/$image.digest"
  done

  for image in "${images[@]}"; do
    local digest zot_image ghcr_image
    digest=$(cat "$RELEASE_DIR/$image.digest")
    zot_image="$ZOT/nagi/$image"
    ghcr_image="ghcr.io/happyladysaucem/nagi-$image"
    crane tag "$ghcr_image@$digest" "$SHORT_SHA"
    test "$digest" = "$(crane digest "$ghcr_image:$SHORT_SHA")"
    crane tag "$zot_image@$digest" "$SHORT_SHA"
    test "$digest" = "$(crane digest "$zot_image:$SHORT_SHA")"

    {
      printf '### Nagi-%s %s\n\n' "$image" "$SHORT_SHA"
      printf -- '- Zot `%s:%s`\n- GHCR `%s:%s`\n- Digest `%s`\n\n' \
        "$zot_image" "$SHORT_SHA" "$ghcr_image" "$SHORT_SHA" "$digest"
    } >> "$GITHUB_STEP_SUMMARY"
  done
}

case "${1:-}" in
  stage) stage_images ;;
  promote) promote_images ;;
  *) echo '用法：bash ci/package-images.sh stage|promote' >&2; exit 2 ;;
esac
