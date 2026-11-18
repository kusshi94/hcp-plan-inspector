# hcp-plan-inspector

`terraform login` で保存された認証情報を使って、HCP Terraform (Terraform Cloud) 上の
Plan 内容を CLI から確認できる read-only なツールです。

Apply を HCP Terraform 経由で行っているリポジトリでは、GitHub の PR diff だけでは
「実際に何が変わるか」、特に **何が destroy されるか** が分かりません。1 つの PR に
複数 workspace の Run がぶら下がることも多く、都度ブラウザで開いて確認するのは手間です。
このツールは PR 番号(または Run ID)を渡すだけで、それらをまとめて表示します。

読み込む API は Run / Plan の取得とログのダウンロードのみ(すべて GET)です。
Apply / Discard / Cancel など状態を変更する API は呼びません。Apply は引き続き
HCP Terraform 上で人間が `Confirm Apply` して行ってください。

## Install

### Go install

```console
go install github.com/kusshi94/hcp-plan-inspector/cmd/hcp-plan-inspector@latest
```

### GitHub Releases

[Releases](https://github.com/kusshi94/hcp-plan-inspector/releases) から OS/アーキテクチャに
合ったバイナリをダウンロードして `PATH` の通ったディレクトリに配置してください。

### ソースからビルド

```console
git clone https://github.com/kusshi94/hcp-plan-inspector.git
cd hcp-plan-inspector
go build -o hcp-plan-inspector ./cmd/hcp-plan-inspector
```

### Nix

Flake を用意しています。ビルドせずに直接実行することもできます。

```console
nix run github:kusshi94/hcp-plan-inspector -- --help
```

環境に入れたい場合は `nix profile install github:kusshi94/hcp-plan-inspector`、
自分の flake から参照したい場合は inputs に加えて
`inputs.hcp-plan-inspector.packages.${system}.default` を参照してください。

## 事前準備

- `terraform login` を実行済みであること(トークンの探索順は [Token](#token) 参照)
- PR 番号を指定する場合のみ、[`gh`](https://cli.github.com/) がインストール・認証済みであること
  (Run ID を `-r` で直接指定する場合は不要)

## Usage

```console
hcp-plan-inspector --org <organization> <PR番号> [flags]
hcp-plan-inspector --org <organization> -r <Run ID> [-r <Run ID> ...] [flags]
```

### 例

```console
# PR にぶら下がる全 workspace の plan
hcp-plan-inspector --org example-org 123

# workspace 名で絞り込む (部分一致)
hcp-plan-inspector --org example-org 123 -w production

# Run ID を直接指定する (gh 不要)
hcp-plan-inspector --org example-org -r run-example00000001

# 機械可読な JSON で出力し、後段で加工する / AI エージェントに読ませる
hcp-plan-inspector --org example-org 123 --json | jq '...'
```

`update` / `replace` のリソースは、変更される属性を `属性: 変更前 -> 変更後` の形で表示します
(`create` / `delete` は変更量が多いため一覧のみ。`--full` を付けると `create` / `delete` も属性を表示します)。destroy / replace されるリソースは実行結果の
末尾でもまとめて件数が出るので見落としにくくなっています。`sensitive` とマークされた値は
片側だけマークされていても両側とも `(sensitive)` に伏せて表示します。

人間向け出力では、1 リソースあたり 20 行を超える差分と 80 文字を超える値を省略します
(省略した場合は末尾に案内が出ます)。全件見るには `--full` を付けてください。
`--json` は常に省略せずに出力します。`--json` でも `create` / `delete` の属性を出したい場合は `--full` を併用してください。

### Flags

| フラグ | 説明 |
| --- | --- |
| `<PR番号>` (位置引数) | PR の checks から Terraform Cloud の Run をすべて辿る (`gh` を使用) |
| `-r`, `--run <Run ID>` | Run ID を直接指定する (複数指定可) |
| `-w`, `--workspace <文字列>` | workspace 名で絞り込む (部分一致) |
| `--org <organization>` | organization を指定する (環境変数 `TFC_ORG` でも指定可、いずれか必須) |
| `--json` | 機械可読な JSON で出力する |
| `--raw` | 構造化ログの生データを出力する |
| `--full` | 差分の行数・値の長さを省略せずに表示し、`create` / `delete` の属性も表示する (`--json` と併用可) |
| `--no-detail` | 属性差分 (`update` / `replace`、`--full` 時は `create` / `delete` の詳細) を取得しない。workspace 数が多い PR で高速化したいとき向け |
| `-h`, `--help` | ヘルプを表示する |
| `-v`, `--version` | バージョンを表示する |

属性差分の取得(`update` / `replace`、`--full` 時は `create` / `delete` の詳細)には workspace への **admin 権限**が必要です。
権限が無い場合は警告を出したうえで、リソースの一覧までは表示します。

### Token

HCP Terraform のトークンは以下の順で探します。見つからない場合は `terraform login` を
案内して終了します。

1. 環境変数 `TF_TOKEN_app_terraform_io`
2. 環境変数 `TFC_CREDENTIALS_FILE` が指すファイル
3. `~/.terraform.d/credentials.tfrc.json` (`terraform login` の保存先)

### 終了コード

- `0` — 取得に成功した(plan 自体が `errored` の場合も `0`。内容は出力を見て判断してください)
- `1` — 引数・認証・API・ネットワークのエラー
