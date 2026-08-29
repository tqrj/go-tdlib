# 生成代码的 schema 来源是本地的 tqrj/tdPatch（TDLib 1.8.67 + 指纹补丁），不是上游 tdlib/td：
# tdPatch 的 commit 在 GitHub 的 tdlib/td 下不存在，按 -version 下载会 404，所以生成器
# 改成读本地文件（-schema / -code）。TD_DIR 指向 tdPatch 的工作目录，TAG 只用来记录版本。
TD_DIR ?= /Users/coca/Cproject/tdPatch
TAG := $(shell git -C $(TD_DIR) rev-parse HEAD)

schema-update:
	cp $(TD_DIR)/td/generate/scheme/td_api.tl ./data/td_api.tl

generate-json:
	go run ./cmd/generateJson/main.go \
		-version "$(TAG)" \
		-schema "$(TD_DIR)/td/generate/scheme/td_api.tl" \
		-code "$(TD_DIR)/td/telegram/Requests.cpp" \
		-output "./data/td_api.json"

generate-code:
	go run ./cmd/generateCode/main.go \
		-version "$(TAG)" \
		-schema "$(TD_DIR)/td/generate/scheme/td_api.tl" \
		-code "$(TD_DIR)/td/telegram/Requests.cpp" \
		-outputDir "./client" \
		-package client \
		-functionFile function_generated.go \
		-typeFile type_generated.go \
		-unmarshalerFile unmarshaler_generated.go \
		-versionFile version_generated.go
	go fmt ./...

generate: schema-update generate-json generate-code
