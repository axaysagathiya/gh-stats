# GitHub Profile Contribution Stats Tracker (`gh-stats`)

`gh-stats` is a fast, lightweight, and cache-efficient CLI tool written in Go that gathers your pull request contributions from external repositories and displays them as a neat stats table. It can be run locally or integrated into any GitHub repository (such as your personal **GitHub Profile README**) to automatically update your statistics on a recurring schedule.

---

## Key Features

- **Smart Caching (Speed & Rate-Limit Friendly)**: Stores fetched data locally in a compact GOB binary format (`github_cache_<username>.gob`). This prevents redundant API requests and avoids hitting GitHub GraphQL rate limits.
- **Smart Synchronization**:
  - **Full History Scan**: Automatically runs on the first execution, querying history year-by-year starting from the year your GitHub account was created.
  - **Incremental Sync**: Subsequent runs query only for PRs updated since the last sync, meaning runs complete in a fraction of a second.
- **Robust Error Handling**: If GitHub's API is down, your token expires, or you run out of quota, the tool gracefully exits without saving or advancing the sync timestamp. This prevents gaps/holes in your cache.
- **Terminal Clickable Links**: Renders an interactive table directly in your terminal using clickable hyperlinks to quickly open target repositories.
- **Zero-Setup Action Integration**: Packaged as a Composite GitHub Action, allowing anyone to integrate it into their repository with just a few lines of YAML.

---

## Configuration Options

When running the application (either locally or via GitHub Actions), the following options are supported:

| Flag / Parameter | Shortcut | Description | Default |
| :--- | :---: | :--- | :--- |
| `<username>` | — | **(Required)** The GitHub username to fetch stats for. Can be a raw username or a GitHub profile URL. | — |
| `--force` | `-f` | Discards the existing `.gob` cache file and forces a full history resync. | `false` |
| `--readme <path>` | `-r` | Specifies a custom file path for the target markdown file to inject stats into. If not specified, stats are printed to the terminal and no files are modified. | `""` (Do not update any files) |

---

## How to Run Locally

### 1. Prerequisites
- **Go**: Ensure Go 1.16+ is installed (`go version`).
- **GitHub Personal Access Token (PAT)**:
  - Generate a classic PAT under GitHub **Settings** -> **Developer settings** -> **Personal access tokens** -> **Tokens (classic)**.
  - The token requires the **`repo`** scope (or public read access to repos).

### 2. Execution

Set the `GH_STATS_TOKEN` environment variable and run the main entry point:

```bash
# Basic run (prints stats to the terminal, does not modify any files)
GH_STATS_TOKEN=your_token_here go run main.go axaysagathiya

# Force a full resync (rebuild cache from scratch)
GH_STATS_TOKEN=your_token_here go run main.go --force axaysagathiya

# Update a specific README file (injects stats between comment markers)
GH_STATS_TOKEN=your_token_here go run main.go --readme /path/to/target/README.md axaysagathiya
```

---

## How to Set Up for Your GitHub Profile README

You can use GitHub Actions to automatically update the stats table on your main GitHub Profile page (the README displayed at `github.com/your-username`).

### Step 1: Add HTML Comment Markers to your Profile README
In the `README.md` file of your profile repository (the repository named exactly after your username, e.g. `axaysagathiya/axaysagathiya`), add the following comment markers where you want the table to appear:

```markdown
<!-- START_STATS -->
<!-- END_STATS -->
```

### Step 2: Create a GitHub Personal Access Token (PAT)
GitHub's default temporary `GITHUB_TOKEN` in workflows does not have permissions to query your contribution details across other (external) organizations/repositories. 
1. Go to your GitHub account **Settings** -> **Developer settings** -> **Personal access tokens** -> **Tokens (classic)**.
2. Click **Generate new token (classic)**.
3. Select the **`repo`** scope.
4. Generate and copy the token.

### Step 3: Add the Token to your Profile Repository Secrets
1. Go to your profile repository on GitHub (e.g., `github.com/your-username/your-username`).
2. Click **Settings** (top bar) -> **Secrets and variables** (left sidebar) -> **Actions**.
3. Click **New repository secret**.
4. Set the name to **`GH_STATS_TOKEN`** and paste your Personal Access Token as the value.

### Step 4: Create the GitHub Actions Workflow
In your profile repository, create a directory structure `.github/workflows/` and add a file named `update-stats.yml` with the following configuration (replace `your-github-username` with your username):

```yaml
name: Update GitHub Stats

on:
  schedule:
    - cron: '0 */12 * * *' # Run automatically every 12 hours
  workflow_dispatch:      # Allow manual trigger from the GitHub Actions tab

permissions:
  contents: write         # Allows the Action to commit and push updates back to your repository

jobs:
  update-readme:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout Repository
        uses: actions/checkout@v4

      - name: Update Stats Table
        uses: axaysagathiya/gh-stats@v1
        with:
          token: ${{ secrets.GH_STATS_TOKEN }}
          # username: 'your-github-username' # Optional (defaults to the repository owner)
          # readme_path: 'README.md'         # Optional (defaults to README.md)
```

Push this workflow file to your default branch. The workflow will run automatically every 12 hours. You can also run it immediately by navigating to the **Actions** tab on your GitHub repository, selecting **Update GitHub Stats**, and clicking **Run workflow**.
