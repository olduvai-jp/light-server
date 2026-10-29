# light-server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 合意済みの常駐配信・保存サーバーを実装して動作確認する。
**Architecture:** Goの単一実行ファイル。プロジェクトごとにSQLiteとファイル保存。CLIはHTTP APIを利用する。
**Tech Stack:** Go、net/http、SQLite。
**Spec:** docs/superpowers/specs/2026-10-30-light-server-design.md

## Global Constraints

localhost待受、バックアップなし、認証なし、登録解除では保持、配信元は削除しない。

## Review Focus

パス逸脱、転送中断、再起動後の保持、同一IDのプロジェクト分離、検索値の型と文字列の扱いを検証する。

### Task 1: サーバー・ストレージ

- [x] main.go、server.go、store.goを作成する。管理API、静的配信、CRUD・検索、ストリーミング転送を実装。
- [x] server_test.goで一時保存先を使いプロジェクト分離、CRUD、検索、Range、登録解除・再登録、パス逸脱を検証。
- [x] go test ./... と go vet ./... が成功することを確認。

### Task 2: CLI・運用

- [x] 管理CLI、README.md、systemd設定を追加。HTTP APIとJavaScriptの例を文書化。
- [x] ビルド後の実行ファイルからサーバーを起動し、CLI登録、HTTP転送、再起動保持を確認。
- [x] 利用可能な権限で常駐サービスを導入し、待ち受けとサービス状態を確認。

### Task 3: レビュー

- [x] 設計書との対応をレビューし、指摘修正後に関連チェックを再実行。
