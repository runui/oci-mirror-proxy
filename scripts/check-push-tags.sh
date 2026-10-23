#!/bin/sh
set -eu

remote=${1:?Missing push remote}

while read -r local_ref local_oid remote_ref remote_oid; do
	case "$remote_ref" in
		refs/tags/*) ;;
		*) continue ;;
	esac
	# Deleting a tag does not publish a commit.
	case "$local_oid" in
		*[!0]*) ;;
		*) continue ;;
	esac

	if ! git remote get-url "$remote" >/dev/null 2>&1; then
		printf '%s\n' "Cannot check tag $remote_ref: use a named remote with local remote-tracking branches instead of a URL." >&2
		exit 1
	fi
	if ! commit=$(git rev-parse --verify "$local_oid^{commit}" 2>/dev/null); then
		printf '%s\n' "Cannot push $remote_ref: the tag must point to a commit." >&2
		exit 1
	fi
	if ! branches=$(git for-each-ref --format='%(refname)' --contains "$commit" "refs/remotes/$remote/"); then
		exit 1
	fi
	if [ -z "$branches" ]; then
		printf '%s\n' "Cannot push $remote_ref: commit $commit is not in any local remote-tracking branch for $remote." \
			"Push the branch to $remote first, then push the tag. If it is already pushed, run git fetch $remote and retry." >&2
		exit 1
	fi
done
