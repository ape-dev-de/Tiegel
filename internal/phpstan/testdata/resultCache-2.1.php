<?php declare(strict_types = 1);

return [
	'lastFullAnalysisTime' => 1795680783,
	'meta' => array (
  'cacheVersion' => 'v12-linesToIgnore',
  'phpstanVersion' => '2.1.56',
  'fnsr' => false,
  'metaExtensions' => 
  array (
  ),
  'phpVersion' => 80510,
  'projectConfig' => '{parameters: {level: 6, paths: [/__ROOT__/src], tmpDir: /__ROOT__/build/phpstan-tmp}}',
  'analysedPaths' => 
  array (
    0 => '/__ROOT__/src',
  ),
  'scannedFiles' => 
  array (
  ),
  'composerLocks' => 
  array (
    '/__ROOT__/composer.lock' => 'eeed3a8eb238a7e387d4b49b756703285d7be773accbe588360681ef2307d866',
  ),
  'composerInstalled' => 
  array (
    '/__ROOT__/vendor/composer/installed.php' => 
    array (
      'versions' => 
      array (
        'phpstan/phpstan' => 
        array (
          'pretty_version' => '2.1.56',
          'version' => '2.1.56.0',
          'reference' => '93a603c9fc3be8c3c93bbc8d22170ad766685537',
          'type' => 'library',
          'install_path' => '/__ROOT__/vendor/composer/../phpstan/phpstan',
          'aliases' => 
          array (
          ),
          'dev_requirement' => true,
        ),
      ),
    ),
  ),
  'executedFilesHashes' => 
  array (
    'phar:///__ROOT__/vendor/phpstan/phpstan/phpstan.phar/stubs/runtime/Attribute85.php' => 'cb8b31e82c61ce197871c9e8a6f122256751f2ab606dd2be90846d4fa5f8933e',
    'phar:///__ROOT__/vendor/phpstan/phpstan/phpstan.phar/stubs/runtime/ReflectionAttribute.php' => 'c0068e383717870a304781d462f7e2afe1c6f24e9133851852a2aca96b4fa26f',
    'phar:///__ROOT__/vendor/phpstan/phpstan/phpstan.phar/stubs/runtime/ReflectionIntersectionType.php' => '65fe0a8bc6fe285d8ddc8798ab5b9299920af70db5ad74596bc08df823e7c5d9',
    'phar:///__ROOT__/vendor/phpstan/phpstan/phpstan.phar/stubs/runtime/ReflectionUnionType.php' => '1e2fe940e4ba4e00d9ee6adb2af3ee1bf333e6f8afe61c61deb038886d293427',
  ),
  'phpExtensions' => 
  array (
    0 => 'Core',
    1 => 'FFI',
    2 => 'PDO',
    3 => 'PDO_ODBC',
    4 => 'Phar',
    5 => 'Reflection',
    6 => 'SPL',
    7 => 'SimpleXML',
    8 => 'Zend OPcache',
    9 => 'bcmath',
    10 => 'bz2',
    11 => 'calendar',
    12 => 'ctype',
    13 => 'curl',
    14 => 'date',
    15 => 'dba',
    16 => 'dom',
    17 => 'exif',
    18 => 'fileinfo',
    19 => 'filter',
    20 => 'ftp',
    21 => 'gd',
    22 => 'gettext',
    23 => 'gmp',
    24 => 'hash',
    25 => 'iconv',
    26 => 'intl',
    27 => 'json',
    28 => 'ldap',
    29 => 'lexbor',
    30 => 'libxml',
    31 => 'mbstring',
    32 => 'mongodb',
    33 => 'mysqli',
    34 => 'mysqlnd',
    35 => 'odbc',
    36 => 'openssl',
    37 => 'pcntl',
    38 => 'pcre',
    39 => 'pdo_dblib',
    40 => 'pdo_mysql',
    41 => 'pdo_pgsql',
    42 => 'pdo_sqlite',
    43 => 'pgsql',
    44 => 'posix',
    45 => 'random',
    46 => 'readline',
    47 => 'redis',
    48 => 'session',
    49 => 'shmop',
    50 => 'snmp',
    51 => 'soap',
    52 => 'sockets',
    53 => 'sodium',
    54 => 'sqlite3',
    55 => 'standard',
    56 => 'sysvmsg',
    57 => 'sysvsem',
    58 => 'sysvshm',
    59 => 'tidy',
    60 => 'tokenizer',
    61 => 'uri',
    62 => 'xml',
    63 => 'xmlreader',
    64 => 'xmlwriter',
    65 => 'xsl',
    66 => 'zip',
    67 => 'zlib',
  ),
  'stubFiles' => 
  array (
  ),
  'level' => '6',
),
	'projectExtensionFiles' => array (
),
	'errorsCallback' => static function (): array { return array (
  '/__ROOT__/src/Greeter.php' => 
  array (
    0 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Call to an undefined method Acme\\Greeter::missing().',
       'file' => '/__ROOT__/src/Greeter.php',
       'line' => 13,
       'canBeIgnored' => true,
       'filePath' => '/__ROOT__/src/Greeter.php',
       'traitFilePath' => NULL,
       'tip' => NULL,
       'nodeLine' => 13,
       'nodeType' => 'PhpParser\\Node\\Expr\\MethodCall',
       'identifier' => 'method.notFound',
       'metadata' => 
      array (
      ),
       'fixedErrorDiff' => NULL,
    )),
    1 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Parameter #1 $string of function strlen expects string, int given.',
       'file' => '/__ROOT__/src/Greeter.php',
       'line' => 18,
       'canBeIgnored' => true,
       'filePath' => '/__ROOT__/src/Greeter.php',
       'traitFilePath' => NULL,
       'tip' => NULL,
       'nodeLine' => 18,
       'nodeType' => 'PhpParser\\Node\\Expr\\FuncCall',
       'identifier' => 'argument.type',
       'metadata' => 
      array (
      ),
       'fixedErrorDiff' => NULL,
    )),
    2 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Call to an undefined method Acme\\Greeter::alsoMissing().',
       'file' => '/__ROOT__/src/Greeter.php',
       'line' => 23,
       'canBeIgnored' => true,
       'filePath' => '/__ROOT__/src/Greeter.php',
       'traitFilePath' => NULL,
       'tip' => NULL,
       'nodeLine' => 23,
       'nodeType' => 'PhpParser\\Node\\Expr\\MethodCall',
       'identifier' => 'method.notFound',
       'metadata' => 
      array (
      ),
       'fixedErrorDiff' => NULL,
    )),
    3 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Method Acme\\Greeter::items() return type has no value type specified in iterable type array.',
       'file' => '/__ROOT__/src/Greeter.php',
       'line' => 26,
       'canBeIgnored' => true,
       'filePath' => '/__ROOT__/src/Greeter.php',
       'traitFilePath' => NULL,
       'tip' => 'See: https://phpstan.org/blog/solving-phpstan-no-value-type-specified-in-iterable-type',
       'nodeLine' => 26,
       'nodeType' => 'PHPStan\\Node\\InClassMethodNode',
       'identifier' => 'missingType.iterableValue',
       'metadata' => 
      array (
      ),
       'fixedErrorDiff' => NULL,
    )),
    4 => 
    \PHPStan\Analyser\Error::__set_state(array(
       'message' => 'Method Acme\\Greeter::shout() should return int but returns string.',
       'file' => '/__ROOT__/src/Shouting.php (in context of class Acme\\Greeter)',
       'line' => 11,
       'canBeIgnored' => true,
       'filePath' => '/__ROOT__/src/Greeter.php',
       'traitFilePath' => '/__ROOT__/src/Shouting.php',
       'tip' => NULL,
       'nodeLine' => 11,
       'nodeType' => 'PhpParser\\Node\\Stmt\\Return_',
       'identifier' => 'return.type',
       'metadata' => 
      array (
      ),
       'fixedErrorDiff' => NULL,
    )),
  ),
); },
	'locallyIgnoredErrorsCallback' => static function (): array { return array (
); },
	'linesToIgnore' => array (
),
	'unmatchedLineIgnores' => array (
),
	'collectedDataCallback' => static function (): array { return array (
  '/__ROOT__/src/Greeter.php' => 
  array (
    'PHPStan\\Rules\\DeadCode\\MethodWithoutImpurePointsCollector' => 
    array (
      0 => 
      array (
        0 => 'Acme\\Greeter',
        1 => 'items',
        2 => 'Acme\\Greeter',
      ),
    ),
    'PHPStan\\Rules\\Traits\\TraitUseCollector' => 
    array (
      0 => 
      array (
        0 => 'Acme\\Shouting',
      ),
    ),
  ),
  '/__ROOT__/src/Shouting.php' => 
  array (
    'PHPStan\\Rules\\Traits\\TraitDeclarationCollector' => 
    array (
      0 => 
      array (
        0 => 'Acme\\Shouting',
        1 => 7,
      ),
    ),
  ),
); },
	'dependencies' => array (
  '/__ROOT__/src/Greeter.php' => 
  array (
    'fileHash' => 'f5ebcb8be12c4f681e995643b577f48e65bbede731f341a435d8c7db917421b2',
    'dependentFiles' => 
    array (
    ),
  ),
  '/__ROOT__/src/Shouting.php' => 
  array (
    'fileHash' => 'e5ab3204fa5cc25b134fc020be8e36cce85bd9e7ae0834c59104f2579ba1e7e9',
    'dependentFiles' => 
    array (
      0 => '/__ROOT__/src/Greeter.php',
    ),
    'usedTraitDependentFiles' => 
    array (
      0 => '/__ROOT__/src/Greeter.php',
    ),
  ),
),
	'exportedNodesCallback' => static function (): array { return array (
  '/__ROOT__/src/Greeter.php' => 
  array (
    0 => 
    \PHPStan\Dependency\ExportedNode\ExportedClassNode::__set_state(array(
       'name' => 'Acme\\Greeter',
       'phpDoc' => NULL,
       'abstract' => false,
       'final' => true,
       'extends' => NULL,
       'implements' => 
      array (
      ),
       'usedTraits' => 
      array (
        0 => 'Acme\\Shouting',
      ),
       'traitUseAdaptations' => 
      array (
      ),
       'statements' => 
      array (
        0 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'greet',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'string',
           'parameters' => 
          array (
            0 => 
            \PHPStan\Dependency\ExportedNode\ExportedParameterNode::__set_state(array(
               'name' => 'name',
               'type' => 'string',
               'byRef' => false,
               'variadic' => false,
               'hasDefault' => false,
               'attributes' => 
              array (
              ),
               'phpDoc' => NULL,
               'flags' => 0,
            )),
          ),
           'attributes' => 
          array (
          ),
        )),
        1 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'count',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'int',
           'parameters' => 
          array (
          ),
           'attributes' => 
          array (
          ),
        )),
        2 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'ignored',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'int',
           'parameters' => 
          array (
          ),
           'attributes' => 
          array (
          ),
        )),
        3 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'items',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'array',
           'parameters' => 
          array (
          ),
           'attributes' => 
          array (
          ),
        )),
      ),
       'attributes' => 
      array (
      ),
    )),
  ),
  '/__ROOT__/src/Shouting.php' => 
  array (
    0 => 
    \PHPStan\Dependency\ExportedNode\ExportedTraitNode::__set_state(array(
       'name' => 'Acme\\Shouting',
       'phpDoc' => NULL,
       'usedTraits' => 
      array (
      ),
       'traitUseAdaptations' => 
      array (
      ),
       'statements' => 
      array (
        0 => 
        \PHPStan\Dependency\ExportedNode\ExportedMethodNode::__set_state(array(
           'name' => 'shout',
           'phpDoc' => NULL,
           'byRef' => false,
           'public' => true,
           'private' => false,
           'abstract' => false,
           'final' => false,
           'static' => false,
           'returnType' => 'int',
           'parameters' => 
          array (
            0 => 
            \PHPStan\Dependency\ExportedNode\ExportedParameterNode::__set_state(array(
               'name' => 'text',
               'type' => 'string',
               'byRef' => false,
               'variadic' => false,
               'hasDefault' => false,
               'attributes' => 
              array (
              ),
               'phpDoc' => NULL,
               'flags' => 0,
            )),
          ),
           'attributes' => 
          array (
          ),
        )),
      ),
       'attributes' => 
      array (
      ),
    )),
  ),
); },
];
