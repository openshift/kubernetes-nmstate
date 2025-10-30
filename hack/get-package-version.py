#!/usr/bin/env python3
"""
Script to retrieve package versions from OpenShift releases.

Retrieve the version of a given package from one or more OpenShift releases.
If a specific z-stream is provided, only the package version from that
release will be returned. If a y-stream is provided, the script will retrieve
the package version from each z-stream in that y-stream, starting from the
most recently released.
"""

import argparse
import re
import sys
from typing import Optional, List, Tuple
from requests_html import HTMLSession
import urllib


BASE_URL = "https://openshift-release.apps.ci.l2s4.p1.openshiftapps.com"


def fetch_url(url: str, render_js: bool = False) -> str:
    """
    Fetch HTML content from a URL using requests-html.

    Args:
        url: The URL to fetch
        render_js: Whether to render JavaScript (default: False)

    Returns:
        The HTML content

    Raises:
        ConnectionError: If there's a network error
        Exception: If the page cannot be fetched
    """
    session = HTMLSession()
    try:
        response = session.get(url, timeout=30)
        response.raise_for_status()

        if render_js:
            response.html.render(timeout=20, sleep=2)

        return response.html.html
    except Exception as e:
        raise ConnectionError(f"Failed to fetch {url}: {e}")
    finally:
        session.close()


def fetch_release_page(release_version: str) -> str:
    """
    Fetch the HTML content of the OpenShift release page.

    Args:
        release_version: The OpenShift release version (e.g., "4.19.17")

    Returns:
        The HTML content of the release page

    Raises:
        ValueError: If the release version is not found
        ConnectionError: If there's a network error
    """
    url = f"{BASE_URL}/releasestream/4-stable/release/{release_version}"

    try:
        return fetch_url(url)
    except ConnectionError as e:
        if "404" in str(e):
            raise ValueError(f"Release version {release_version} not found")
        raise


def extract_package_version(html_content: str, package_name: str) -> Optional[str]:
    """
    Extract the version of a specific package from the HTML content.

    Args:
        html_content: The HTML content of the release page
        package_name: The name of the package to search for

    Returns:
        The package version string if found, None otherwise
    """
    # Pattern to match package name followed by version
    # Matches formats like: nmstate-2.2.48-2.el9_6
    # The pattern looks for the package name followed by a hyphen and version info
    pattern = rf'\b{re.escape(package_name)}-([0-9][^\s<>"]*?)(?:\s|<|"|$)'

    match = re.search(pattern, html_content)
    if match:
        # Return the full package string (name + version)
        full_match = match.group(0).strip()
        # Clean up any trailing characters
        full_match = re.sub(r'[<>"\s]$', '', full_match)
        return full_match
    else:
        # Try a different format
        pattern = rf'{re.escape(package_name)}</td><td>0</td><td>(\d+\.\d+\.\d+)'
        match = re.findall(pattern, html_content)
        if match:
            return f'{package_name}-{match[0]}'

    return None


def extract_version_from_coreos(html_content: str, package_name: str) -> Optional[str]:
    pattern = rf'to <a.*href=".*stream=(.*)#([^"]*)">'
    match = re.findall(pattern, html_content)
    if match:
        stream = urllib.parse.unquote(match[0][0])
        build_url = f'https://releases-rhcos--prod-pipeline.apps.int.prod-stable-spoke1-dc-iad2.itup.redhat.com/contents.html?stream={stream}&release={match[0][1]}&arch=x86_64'
        build_html = fetch_url(build_url, render_js=True)
        return extract_package_version(build_html, package_name)
    return None


def get_all_patch_releases(minor_version: str) -> List[str]:
    """
    Get all patch releases for a given minor version.

    Args:
        minor_version: Minor version string (e.g., "4.19")

    Returns:
        List of release versions sorted in descending order (newest first)

    Raises:
        ValueError: If the minor version format is invalid
    """
    # Validate minor version format
    if not re.match(r'^\d+\.\d+$', minor_version):
        raise ValueError(f"Invalid minor version format: {minor_version}. Expected format: X.Y (e.g., 4.19)")

    # Fetch the main release page
    url = f"{BASE_URL}/releasestream/4-stable"
    html_content = fetch_url(url)

    # Extract all release versions that match the minor version
    # Pattern matches version strings like "4.19.0", "4.19.17", etc.
    pattern = rf'\b({re.escape(minor_version)}\.\d+)\b'
    matches = re.findall(pattern, html_content)

    # Remove duplicates and sort in descending order (newest first)
    releases = sorted(set(matches), key=lambda v: [int(x) for x in v.split('.')], reverse=True)

    if not releases:
        raise ValueError(f"No releases found for minor version {minor_version}")

    return releases


def main():
    """Main entry point for the script."""
    parser = argparse.ArgumentParser(
        description="Get package version from OpenShift release",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Get package version for a specific release
  %(prog)s nmstate 4.19.17

  # Get package version for all patches in a minor release
  %(prog)s nmstate 4.19

  # Limit results to the 5 most recent releases
  %(prog)s -c 5 nmstate 4.19

  # Use verbose mode
  %(prog)s -v nmstate 4.19
        """
    )

    parser.add_argument(
        'package',
        help='Name of the package to search for'
    )

    parser.add_argument(
        'release',
        help='OpenShift release version (e.g., 4.19.17 for specific release, or 4.19 for all patches)'
    )

    parser.add_argument(
        '-v', '--verbose',
        action='store_true',
        help='Show verbose output'
    )

    parser.add_argument(
        '-c', '--count',
        type=int,
        metavar='N',
        help='Limit the number of results returned (only applies to minor version queries)'
    )

    args = parser.parse_args()

    try:
        # Determine if this is a minor version (X.Y) or full version (X.Y.Z)
        is_minor_version = bool(re.match(r'^\d+\.\d+$', args.release))

        if is_minor_version:
            # Get all patch releases for the minor version
            if args.verbose:
                print(f"Fetching all patch releases for {args.release}...", file=sys.stderr)

            releases = get_all_patch_releases(args.release)

            # Apply count limit if specified
            if args.count and args.count > 0:
                original_count = len(releases)
                releases = releases[:args.count]
                if args.verbose:
                    print(f"Found {original_count} releases, limiting to {len(releases)}: {', '.join(releases)}", file=sys.stderr)
            elif args.verbose:
                print(f"Found {len(releases)} releases: {', '.join(releases)}", file=sys.stderr)

            if args.verbose:
                print(f"Searching for package '{args.package}' in each release...", file=sys.stderr)

            # Fetch package version for each release
            results = []
            for release in releases:
                if args.verbose:
                    print(f"  Checking {release}...", file=sys.stderr)

                try:
                    html_content = fetch_release_page(release)
                    package_version = extract_package_version(html_content, args.package)

                    # This works on releases 4.19+
                    if package_version:
                        results.append((release, package_version))
                        print(f"{release}: {package_version}")
                    else:
                        package_version = extract_version_from_coreos(html_content, args.package)
                        if package_version:
                            results.append((release, package_version))
                            print(f"{release}: {package_version}")
                    if not package_version:
                        print(f"    Package not found in {release}", file=sys.stderr)
                except ValueError as e:
                    if args.verbose:
                        print(f"    Skipping {release}: {e}", file=sys.stderr)
                    continue

            # Output results
            if results:
                return 0
            else:
                print(f"Error: Package '{args.package}' not found in any release of {args.release}",
                      file=sys.stderr)
                return 1

        else:
            # Single release version
            if args.verbose:
                print(f"Fetching release information for {args.release}...", file=sys.stderr)

            html_content = fetch_release_page(args.release)

            if args.verbose:
                print(f"Searching for package: {args.package}...", file=sys.stderr)

            package_version = extract_package_version(html_content, args.package)

            if package_version:
                print(package_version)
                return 0
            else:
                # This method is needed for releases prior to 4.19
                package_version = extract_version_from_coreos(html_content, args.package)
                if package_version:
                    print(package_version)
                    return 0
            print(f"Error: Package '{args.package}' not found in release {args.release}",
                    file=sys.stderr)
            return 1

    except ValueError as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1
    except ConnectionError as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1
    except Exception as e:
        print(f"Unexpected error: {e}", file=sys.stderr)
        if args.verbose:
            import traceback
            traceback.print_exc(file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())

