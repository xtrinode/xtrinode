#!/usr/bin/env ruby
# frozen_string_literal: true

require "yaml"

SKIPPED_PATH_PATTERNS = [
  %r{\Ahelm/[^/]+/templates/},
  %r{\Ahelm/[^/]+/charts/}
].freeze

failed = []
checked = 0

ARGV.each do |path|
  next if SKIPPED_PATH_PATTERNS.any? { |pattern| path.match?(pattern) }

  checked += 1
  YAML.parse_file(path)
rescue Psych::SyntaxError => e
  failed << "#{path}: #{e.message}"
end

if failed.empty?
  puts "Parsed #{checked} YAML files"
  exit 0
end

warn "YAML syntax errors:"
failed.each { |message| warn "  #{message}" }
exit 1
