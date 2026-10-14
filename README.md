# slackadm

Free / ProのSlackで、本人の権限によるチャンネル整理とメンバー所属管理を行うCLIです。
Go + Cobra、Windows / macOS / Linux対応。公式Slack Web APIのみを利用します。

```sh
go install github.com/kurotch321/slackadm/cmd/slackadm@latest
# このチェックアウトからビルドする場合
go build -o slackadm ./cmd/slackadm
```

リリース前の初版です。現時点ではモック検証のみで、実SlackのOAuth・変更操作は未検証です。

## 認証

1. [Slackアプリ作成画面](https://api.slack.com/apps)で **From a manifest** を選び、
   [slack-app-manifest.json](slack-app-manifest.json)を読み込みます。
2. OAuth & PermissionsでPKCEが有効、Redirect URLが
   `http://localhost:17839/callback`、Bot scopesが空であることを確認します。
3. Basic Informationの **Client ID** でログインします。Client Secretは不要です。

```sh
slackadm login --client-id 123456.789012 --profile work
slackadm auth status --profile work
slackadm doctor --profile work
```

[SlackのPKCE仕様](https://docs.slack.dev/authentication/using-pkce/)では、PKCE有効化は
サポートへの依頼なしでは元に戻せません。公開クライアントのrefresh tokenは30日で期限切れに
なるため、その場合は再ログインしてください。アプリのインストールに管理者の承認が必要な
ワークスペースもあります。

トークンはOSの資格情報ストアに保存します。LinuxはSecret Serviceとアンロック済みの
キーリングが必要です。利用できない環境では保存せず、`SLACKADM_TOKEN`を使用してください。
refresh tokenはプロファイル単位のプロセス間ロックの下で更新します。

CIでは秘密変数からUser OAuth Tokenを`SLACKADM_TOKEN`に設定します。この変数が存在すれば
保存済み認証より優先し、空値・認証失敗時もフォールバックしません。
`auth logout`は選択プロファイルのローカル認証だけを削除し、Slack上の失効や環境変数の変更は
行いません。

## コマンド例

```sh
slackadm channels list --visibility public --archived false
slackadm channels info example-project
slackadm channels members list CEXAMPLE
slackadm users search example
slackadm users list --active true --admin false
slackadm users info person@example.invalid
slackadm users channels UEXAMPLE

slackadm channels create example-project --dry-run
slackadm channels create example-project --private
slackadm channels rename CEXAMPLE example-renamed
slackadm channels topic CEXAMPLE "Project coordination"
slackadm channels purpose CEXAMPLE "Example project"
slackadm channels members add CEXAMPLE person@example.invalid
slackadm channels members remove CEXAMPLE UEXAMPLE --yes
slackadm channels archive CEXAMPLE --dry-run
slackadm channels archive CEXAMPLE --yes
slackadm channels unarchive CEXAMPLE
slackadm channels join CEXAMPLE
slackadm channels leave CEXAMPLE --yes
```

変更対象はID・完全一致チャンネル名・完全一致メールに限定します。表示名でユーザーを変更
できません。非公開情報を推測せず、取得できる範囲だけ返します。ユーザーが権限を持たない
操作は、スコープがあっても実行できません。新規ワークスペース招待、アカウント無効化、
Enterprise管理API、メッセージ操作、MCPサーバーは対象外です。

## batch

```sh
slackadm batch plan --file examples/operations.json --save plan.json
slackadm batch show --file plan.json
slackadm batch apply --file plan.json --dry-run
slackadm batch apply --file plan.json --yes
```

入力は`[{"command":"channels archive","args":{"channel":"CEXAMPLE"}}]`形式です。
計画には解決済みID、ワークスペース、実行ユーザー、関連する変更前状態を保存します。
`--save`は既存ファイルを上書きしません。stdoutは共通JSON形式なので、リダイレクトした結果を
そのままplanファイルとして使う代わりに`--save`を利用してください。

適用時に環境と状態を確認し、順次実行します。同じチャンネルへの連続操作は自身の成功した
変更を追跡します。既定では最初の失敗で停止し、`--continue-on-error`で続行できます。
項目ごとに`applied / failed / skipped / dry_run`を返します。自動ロールバックはありません。
計画は署名された承認書ではありません。適用前に内容を確認してください。

Slackには条件付き更新・トランザクションAPIがないため、確認と書き込みの間の競合を完全に
排除できません。作成前の名前確認も、参照できない非公開チャンネルまでは確認できません。

## 実行契約

- stdoutは結果、stderrは診断・進捗。TTYは表形式、それ以外はJSON。
  `--output table|json`で変更できます。
- JSON形式は`schema_version / ok / data / error / meta`。エラーは
  `code / message / slack_code / remedy`を持ちます。
- 終了コード：成功0、入力2、認証3、権限4、API・通信5、部分失敗6、確認が必要7。
- archive・メンバー退出・本人のleave・batch applyは確認対象です。
  非TTYでは入力待ちせず終了7を返します。`--yes`で承認できます。
- すべての変更に`--dry-run`を指定できます。Slack変更・ローカル資格情報変更・計画の保存を
  抑止しますが、参照APIと期限切れ資格情報の更新は行う場合があります。
- 一覧は全ページ取得。`--limit`はフィルター前に走査するレコード数の上限で、
  `meta.truncated`と`meta.next_cursor`を返します。続きは同じ条件に`--cursor`を指定します。
- 429のみ`Retry-After`に従い最大3回再試行（1回の待機上限60秒）。書き込み送信後の通信断、
  不正レスポンス、サーバー内部エラーは`result_unknown`として返し、再送しません。

`slackadm schema`でコマンド・引数・フラグ・スコープ・結果種別をJSONで取得できます。
helpとbatch検証は同じ操作定義を使います。チャンネル用スコープは公開/非公開で異なり、
メール解決には追加で`users:read.email`が必要です。

```sh
slackadm --help
slackadm channels --help
slackadm schema
slackadm completion bash > slackadm.bash
slackadm completion zsh > _slackadm
slackadm completion fish > slackadm.fish
slackadm completion powershell > slackadm.ps1
```

詳しくは[CI・エージェント利用と実環境検証](docs/usage.md)、
[開発ガイド](CONTRIBUTING.md)、[セキュリティ方針](SECURITY.md)を参照してください。
