# CI・エージェント利用と検証

## CI

GitHub Actions等の秘密変数を環境変数に渡します。コマンド引数にトークンを含めないでください。

```yaml
- name: Inspect Slack channels
  env:
    SLACKADM_TOKEN: ${{ secrets.SLACKADM_USER_TOKEN }}
  run: slackadm channels list --output json
```

CIの変更は先にdry-runをレビューし、必要な操作にだけ`--yes`を指定してください。
計画ファイルはチャンネル名・topic・所属などを含みます。公開artifactとして保存しないでください。

## エージェント

1. `slackadm schema`を取得してサポートされる操作と引数を確認します。
2. `slackadm doctor`で認証主体、接続先、取得できたスコープを確認します。
   `scopes_known=false`の場合、不足スコープが空でも権限が充足しているとは限りません。
3. 対象はID、完全一致チャンネル名、完全一致メールで指定します。
   `users channels`はSlackの仕様上、実行ユーザーと対象ユーザーが共に参加する
   チャンネルだけを返します。ワークスペース全体の所属一覧ではありません。
4. 変更案は`--dry-run`または`batch plan --save`でレビュー可能にします。
5. 承認済みの変更を`--yes`で実行し、終了コードと項目別結果を確認します。
6. `result_unknown`を受け取ったら現在の状態を参照し、無条件に再実行しません。

## ローカル検証

```sh
go test ./...
go vet ./...
go test -race -coverprofile=coverage.out ./...
go build ./cmd/slackadm
```

通常のテストは実Slackや秘密情報に依存しません。SDK `slack-go/slack` のOAuth型と
`slacktest`を利用し、`httptest`でレート制限・通信断・ページング等を検証します。
CLIバイナリを子プロセス起動するE2Eテストも含みます。

CIは3 OSでテスト・vet・ビルドし、Linuxでrace検査とカバレッジを実行します。
Security workflowはgovulncheck、CodeQLはGo解析、PRでは依存関係レビューを実施します。
外部ActionsはコミットSHAで固定。fork PRに秘密情報は提供しません。
DependabotはGoとActionsを週次・種類別で更新し、自動マージは有効にしません。

## 実Slackでの検証手順（未実施）

認証環境と専用テストチャンネルが提供されていないため、実環境検証は行っていません。
テスト用ワークスペースで以下を実行し、日時・CLIバージョン・結果だけ記録してください。

1. manifestからアプリを作り、ブラウザーで`login`。`auth status`と`doctor`を確認。
2. 同じUser TokenをCIの秘密変数として設定し、チャンネル・ユーザー参照を確認。
3. 明示的に選んだテスト名でチャンネル作成、名称・topic・purpose変更を確認。
4. 同意したテストユーザーの追加・退出、本人のjoin/leaveを確認。
5. 専用チャンネルのarchive/unarchiveを確認。他のチャンネルを対象にしない。
6. batchのdry-run、適用、計画後の変更検出、別プロファイルでの環境不一致を確認。
7. refresh token更新、logout、環境変数優先を確認。ログにトークンを記録しない。

## リリースとブランチ

`v*`タグでGoReleaserが3 OS × amd64/arm64のアーカイブとチェックサムを作り、
GitHubのドラフトリリースに添付します。公開はメンテナーの別操作です。

ブランチ保護の推奨設定：mainはPR経由、3 OSのCI成功必須、承認数0、
force pushと削除を禁止。設定の有効状態はGitHubのSettingsで確認してください。
