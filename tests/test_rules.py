"""Rule conversion semantics; no downloads or device writes."""
import importlib.util
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]/'tools'))
spec = importlib.util.spec_from_file_location('rules', Path(__file__).resolve().parents[1]/'tools/build-rules.py')
rules = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rules)


class RuleConversion(unittest.TestCase):
    def test_cn_ranges_merge_without_absorbing_foreign_addresses(self):
        self.assertEqual(rules.cn_ranges('1.0.0.0,1.0.0.127,CN\n1.0.0.128,1.0.0.255,CN\n1.0.1.0,1.0.1.255,US\n'),
                         {'ip_cidr': ['1.0.0.0/24']})

    def test_reversed_empty_or_non_ipv4_ranges_rejected(self):
        for text in ['1.0.0.1,1.0.0.0,CN', '1.0.0.0,1.0.0.255,US', '::1,::2,CN', 'bad']:
            with self.assertRaises(ValueError): rules.cn_ranges(text)

    def test_nested_include_filters_and_rule_kinds(self):
        files = {'cn': 'include:middle @cn @-ads\nplain.example\nfull:exact.example\nkeyword:word\nregexp:^abc$ # comment',
                 'middle': 'include:leaf',
                 'leaf': 'domain:china.example @cn\nad.example @cn @ads\nforeign.example @!cn'}
        self.assertEqual(rules.domain_rules(files), {'domain': ['exact.example'], 'domain_keyword': ['word'],
                         'domain_regex': ['^abc$'], 'domain_suffix': ['china.example', 'plain.example']})

    def test_cycles_missing_lists_and_unknown_syntax_fail(self):
        for files in [{'cn': 'include:cn'}, {'cn': 'include:missing'}, {'cn': 'unknown:foo'}, {'cn': 'foo &other'}]:
            with self.assertRaises(ValueError): rules.domain_rules(files)

    def test_order_and_duplicates_do_not_change_result(self):
        self.assertEqual(rules.domain_rules({'cn': 'a.example\nb.example\na.example @cn'}),
                         rules.domain_rules({'cn': 'b.example\na.example'}))


if __name__ == '__main__': unittest.main(verbosity=2)
