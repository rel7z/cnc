import copy
import unittest
from infra.processor_defaults import DEFAULTS, restore_defaults


class DefaultsTests(unittest.TestCase):
    def fixture(self):
        health = {'enabled': True, 'counts': {'running': 0, 'queued': 0}}
        state = {'revision': 'one', 'definitions': [], 'external': []}
        templates = [{'id': identifier, 'definition': {'tool_id': tool, 'folder': 'tools/' + tool}} for tool, identifier in DEFAULTS.items()]
        writes = []
        def call(method, path, data=None):
            if method == 'PUT':
                writes.append((path, copy.deepcopy(data)))
                if path.endswith('processing-state'):
                    health['enabled'] = data['enabled']; return health
                self.assertEqual(path, '/v1/processor-setups')
                self.assertFalse(health['enabled'])
                self.assertEqual(data['revision'], state['revision'])
                state['definitions'].append(data['definition'])
                state['revision'] += '-next'
                return copy.deepcopy(state)
            return copy.deepcopy({'/v1/health': health, '/v1/processor-templates': templates, '/v1/processor-setups': state}[path])
        return call, health, state, templates, writes

    def test_pause_register_and_repeat_without_overwriting_or_enabling(self):
        call, health, state, templates, writes = self.fixture()
        result = restore_defaults(call, pause=True)
        self.assertEqual(result['registered'], ['bf', 'divi'])
        self.assertFalse(health['enabled'])
        self.assertFalse(result['programs_executed'])
        self.assertEqual(len(writes), 3)
        result = restore_defaults(call)
        self.assertEqual(result['registered'], [])
        self.assertEqual(len(writes), 3)

    def test_active_jobs_or_missing_template_prevent_any_registration(self):
        call, health, state, templates, writes = self.fixture()
        health['counts']['queued'] = 1
        with self.assertRaises(RuntimeError): restore_defaults(call, pause=True)
        self.assertEqual(state['definitions'], [])
        health['counts']['queued'] = 0; templates.pop()
        with self.assertRaises(RuntimeError): restore_defaults(call)
        self.assertEqual(state['definitions'], [])


if __name__ == '__main__': unittest.main()
