#!/bin/sh

ai_chats_resolve_go_crypto_backend() {
	AI_CHATS_GO_TAG=""
	ai_chats_crypto_backend="${AI_CHATS_CRYPTO_BACKEND:-goolm}"

	case "$ai_chats_crypto_backend" in
		goolm)
			AI_CHATS_GO_TAG="goolm"
			;;
		libolm)
			;;
		*)
			printf '%s\n' "error: unsupported AI_CHATS_CRYPTO_BACKEND '$ai_chats_crypto_backend' (expected 'goolm' or 'libolm')" >&2
			return 1
			;;
	esac
}
